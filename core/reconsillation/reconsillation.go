package reconsillation

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/ethereum/go-ethereum/common"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/reconsillation/LRUCache"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/reconsillation/helper"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
)

const (
	namedlogger = "log:reconciliation"
)

// accountState tracks the calculated state for an account during reconciliation.
type accountState struct {
	Address         string
	ComputedBalance *big.Int // Absolute balance computed by replaying all transactions (not a delta)
	Nonce           uint64
	GasSpent        *big.Int
}

// Reconciliation handles balance recalculation for tagged accounts after DataSync.
type Reconciliation struct {
	SyncVars    *types.Syncvars
	headerCache LRUCache.LRUCacheInterface
}

// NewReconciliation creates a new Reconciliation instance.
func NewReconciliation() *Reconciliation {
	return &Reconciliation{}
}

// SetSyncVars initializes the reconciliation module with node info and database access.
func (r *Reconciliation) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, wal *WAL.WAL) Reconciliation_router {
	if r.SyncVars == nil {
		r.SyncVars = &types.Syncvars{}
	}
	r.SyncVars.Version = protocolVersion
	r.SyncVars.NodeInfo = nodeInfo
	r.SyncVars.Ctx = ctx
	r.SyncVars.WAL = wal
	return r
}

func (r *Reconciliation) SetSyncVarsConfig(ctx context.Context, config types.Syncvars) Reconciliation_router {
	r.SyncVars = &config
	r.SyncVars.Ctx = ctx
	return r
}

// SetLRUCache sets the LRU cache for block headers. Returns self for chaining.
func (r *Reconciliation) SetLRUCache(cache LRUCache.LRUCacheInterface) Reconciliation_router {
	r.headerCache = cache
	return r
}

// GetLRUCache returns the configured LRU cache (may be nil).
func (r *Reconciliation) GetLRUCache() LRUCache.LRUCacheInterface {
	return r.headerCache
}

// GetSyncVars returns the current sync configuration.
func (r *Reconciliation) GetSyncVars() *types.Syncvars {
	return r.SyncVars
}

func (r *Reconciliation) GetHeaderCache() LRUCache.LRUCacheInterface {
	return r.headerCache
}

func (r *Reconciliation) GetBlockFromLRUCache(blockNumber uint64) (*blockpb.Header, bool) {
	/*
		1. Try to get the block from the LRU Cache
		2. if not found then get from the database using r.SyncVars.NodeInfo.BlockInfo.GetBlockHeaders([]int{int(blockNumber)})
		3. Add the block to the LRU Cache
	*/
	Log.Logger(namedlogger).Info(r.SyncVars.Ctx, "Getting block from LRU cache",
		ion.Uint64("block_number", blockNumber))

	if r.headerCache == nil {
		return nil, false
	}

	block, exist := r.headerCache.Get(blockNumber)
	if exist {
		Log.Logger(namedlogger).Info(r.SyncVars.Ctx, "Cache hit for block",
			ion.Uint64("block_number", blockNumber))
		return block, true
	}

	// 2. Cache miss - get from database using BlockInfo.GetBlockHeaders
	Log.Logger(namedlogger).Info(r.SyncVars.Ctx, "Cache miss - fetching from DB",
		ion.Uint64("block_number", blockNumber))

	if r.SyncVars == nil || r.SyncVars.NodeInfo.BlockInfo == nil {
		Log.Logger(namedlogger).Warn(r.SyncVars.Ctx, "Cannot fetch from DB - no BlockInfo configured")
		return nil, false
	}

	headerIterator := r.SyncVars.NodeInfo.BlockInfo.NewBlockHeaderIterator()
	headers, err := headerIterator.GetBlockHeaders([]uint64{blockNumber})
	if err != nil {
		Log.Logger(namedlogger).Error(r.SyncVars.Ctx, "Failed to fetch block from DB", err,
			ion.Uint64("block_number", blockNumber))
		return nil, false
	}

	if len(headers) == 0 {
		Log.Logger(namedlogger).Warn(r.SyncVars.Ctx, "Block not found in DB",
			ion.Uint64("block_number", blockNumber))
		return nil, false
	}

	header := headers[0]

	// 3. Add the block to the LRU Cache
	r.headerCache.Put(blockNumber, header)
	Log.Logger(namedlogger).Info(r.SyncVars.Ctx, "Added block to cache",
		ion.Uint64("block_number", blockNumber))

	return header, true
}

// Reconcile calculates updated balances for all tagged accounts and commits them atomically.
//
// Three-phase execution:
//  1. Concurrently compute the new balance for every account — results held in memory.
//  2. Write every planned change to the WAL and flush — enables crash recovery.
//  3. Atomically commit all updates to the DB in one transaction (all or none).
//
// Returns the number of accounts reconciled and the list of any accounts that failed
// during the computation phase. A non-nil error always means the DB was NOT mutated.
func (r *Reconciliation) Reconcile(taggedAccounts *taggingpb.TaggedAccounts) (int, []string, error) {
	if taggedAccounts == nil || len(taggedAccounts.Accounts) == 0 {
		Log.Logger(namedlogger).Info(r.SyncVars.Ctx, "No tagged accounts to reconcile")
		return 0, nil, nil
	}

	// Use the stored context so cancellation propagates from the caller
	ctx, cancel := context.WithCancel(r.SyncVars.Ctx)
	defer cancel()

	numAccounts := len(taggedAccounts.Accounts)
	Log.Logger(namedlogger).Info(ctx, "Starting reconciliation",
		ion.Int("tagged_accounts_count", numAccounts))

	// ----------------------------------------------------------------
	// Phase 1: Concurrently compute all account states — no DB writes
	// ----------------------------------------------------------------
	numRoutines := min(numAccounts, constants.ATMOST_ACCOUNT_ROUTINES)
	batchSize := (numAccounts + numRoutines - 1) / numRoutines // ceiling division

	batches := helper.SplitIntoBatches(taggedAccounts, batchSize)
	accountManager := r.SyncVars.NodeInfo.BlockInfo.NewAccountManager()

	type computeResult struct {
		update types.AccountUpdate
		addr   string
		err    error
	}

	resultCh := make(chan computeResult, numAccounts)
	var wg sync.WaitGroup

	for _, batch := range batches {
		wg.Add(1)
		go func(accounts map[string]bool) {
			defer wg.Done()
			for addr := range accounts {
				update, err := r.computeAccountUpdate(accountManager, addr)
				resultCh <- computeResult{update: update, addr: addr, err: err}
			}
		}(batch)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	updates := make([]types.AccountUpdate, 0, numAccounts)
	var failedAccounts []string
	var computeErrs []error

	for res := range resultCh {
		if res.err != nil {
			failedAccounts = append(failedAccounts, res.addr)
			computeErrs = append(computeErrs, res.err)
		} else {
			updates = append(updates, res.update)
		}
	}

	if len(computeErrs) > 0 {
		Log.Logger(namedlogger).Warn(ctx, "Some accounts failed during computation phase — aborting commit",
			ion.Int("failed_count", len(computeErrs)),
			ion.Int("succeeded_count", len(updates)))
		return 0, failedAccounts, fmt.Errorf("computation phase failed for %d accounts, no DB changes were made: %v",
			len(computeErrs), computeErrs)
	}

	Log.Logger(namedlogger).Info(ctx, "Computation phase complete — all account states ready",
		ion.Int("accounts_ready", len(updates)))

	// ----------------------------------------------------------------
	// Phase 2: Write all planned changes to WAL and flush
	// ----------------------------------------------------------------
	if r.SyncVars.WAL != nil {
		for _, u := range updates {
			walEvent := &WAL.ReconciliationEvent{
				AccountAddress: u.Address,
				NewBalance:     u.NewBalance.String(),
				Nonce:          u.Nonce,
				Timestamp:      time.Now().Unix(),
			}
			if _, err := r.SyncVars.WAL.WriteEvent(walEvent); err != nil {
				return 0, nil, fmt.Errorf("WAL write failed for account %s — aborting commit: %w", u.Address, err)
			}
		}
		if err := r.SyncVars.WAL.Flush(); err != nil {
			return 0, nil, fmt.Errorf("WAL flush failed — aborting commit: %w", err)
		}
		Log.Logger(namedlogger).Info(ctx, "WAL phase complete — all events flushed",
			ion.Int("wal_events", len(updates)))
	}

	// ----------------------------------------------------------------
	// Phase 3: Atomic DB commit — all or none
	// ----------------------------------------------------------------
	if err := accountManager.BatchUpdateAccounts(updates); err != nil {
		return 0, nil, fmt.Errorf("atomic DB commit failed — no accounts were updated: %w", err)
	}

	Log.Logger(namedlogger).Info(ctx, "Reconciliation committed successfully",
		ion.Int("accounts_committed", len(updates)))

	return len(updates), nil, nil
}

// computeAccountUpdate reads all transactions for one account, replays them to get
// the new balance/nonce, and returns a ready-to-commit AccountUpdate.
// This is a read-only operation — it does not touch the DB.
func (r *Reconciliation) computeAccountUpdate(accountManager types.AccountManager, accountAddress string) (types.AccountUpdate, error) {
	transactions, err := accountManager.GetTransactionsForAccount(accountAddress)
	if err != nil {
		return types.AccountUpdate{}, fmt.Errorf("failed to get transactions for account %s: %w", accountAddress, err)
	}

	state := r.calculateAccountState(accountAddress, transactions)

	currentBalance, _, err := accountManager.GetAccountBalance(accountAddress)
	if err != nil {
		return types.AccountUpdate{}, fmt.Errorf("failed to get current balance for account %s: %w", accountAddress, err)
	}

	// Guard against nil balance for accounts not yet in the DB
	if currentBalance == nil {
		currentBalance = big.NewInt(0)
	}

	newBalance := state.ComputedBalance
	if newBalance.Sign() < 0 {
		newBalance = big.NewInt(0) // Prevent negative balances
	}

	return types.AccountUpdate{
		Address:      accountAddress,
		NewBalance:   newBalance,
		Nonce:        state.Nonce,
		IsNewAccount: currentBalance.Sign() == 0,
	}, nil
}

// calculateAccountState replays all transactions for an account to compute its
// absolute balance and highest outgoing nonce.
//
// For each transaction the account may play up to four roles simultaneously:
//   - Sender   (fromAddr == account): nonce advances, full gas fee deducted, value deducted
//   - Receiver (toAddr   == account): value credited
//   - Coinbase (block.CoinbaseAddr == account): half+remainder of the gas fee credited
//   - ZKVM     (block.ZkvmAddr    == account): half of the gas fee credited
func (r *Reconciliation) calculateAccountState(accountAddress string, transactions []types.DBTransaction) *accountState {
	// Normalize once — common.Address.Hex() returns EIP-55 checksummed mixed-case;
	// DB addresses are often lowercase. Compare everything lowercase to be safe.
	normalizedAccount := strings.ToLower(accountAddress)

	state := &accountState{
		Address:         accountAddress,
		ComputedBalance: big.NewInt(0),
		Nonce:           0,
		GasSpent:        big.NewInt(0),
	}

	for _, tx := range transactions {
		fromAddr := ""
		if tx.From != nil {
			fromAddr = strings.ToLower(tx.From.Hex())
		}

		toAddr := ""
		if tx.To != nil {
			toAddr = strings.ToLower(tx.To.Hex())
		}

		isOutgoing := fromAddr == normalizedAccount
		isIncoming := toAddr == normalizedAccount

		// ------------------------------------------------------------------
		// Gas fee: compute total, then split between coinbase and ZKVM.
		// Done for every tx — even zero-value ones — because gas is always paid.
		// ------------------------------------------------------------------
		gasFeeToDeduct := r.calculateGasCostFromTx(&tx)

		halfGasFee := new(big.Int).Div(gasFeeToDeduct, big.NewInt(2))
		remainder := new(big.Int).Mod(gasFeeToDeduct, big.NewInt(2))
		// coinbase gets half + remainder (accounts for odd-wei rounding)
		// zkvm gets the exact half
		coinbaseGasFee := new(big.Int).Add(halfGasFee, remainder)
		zkvmGasFee := new(big.Int).Set(halfGasFee)

		// Resolve coinbase and ZKVM addresses for this block
		var coinbaseAddr, zkvmAddr string
		if header, ok := r.GetBlockFromLRUCache(tx.BlockNumber); ok {
			if len(header.GetCoinbaseAddr()) > 0 {
				coinbaseAddr = strings.ToLower(common.BytesToAddress(header.GetCoinbaseAddr()).Hex())
			}
			if len(header.GetZkvmAddr()) > 0 {
				zkvmAddr = strings.ToLower(common.BytesToAddress(header.GetZkvmAddr()).Hex())
			}
		}

		// ------------------------------------------------------------------
		// Sender role: nonce advances, gas deducted, value deducted
		// Nonce and gas apply to ALL outgoing txs — including zero-value calls
		// ------------------------------------------------------------------
		if isOutgoing {
			if tx.Nonce > state.Nonce {
				state.Nonce = tx.Nonce
			}
			state.GasSpent.Add(state.GasSpent, gasFeeToDeduct)
			state.ComputedBalance.Sub(state.ComputedBalance, gasFeeToDeduct)
		}

		// ------------------------------------------------------------------
		// Value transfer: only when value is non-nil and positive
		// ------------------------------------------------------------------
		if tx.Value != nil && tx.Value.Sign() > 0 {
			if isOutgoing {
				state.ComputedBalance.Sub(state.ComputedBalance, tx.Value)
			}
			if isIncoming {
				state.ComputedBalance.Add(state.ComputedBalance, tx.Value)
			}
		}

		// ------------------------------------------------------------------
		// Gas fee credit: coinbase and ZKVM receive their share
		// ------------------------------------------------------------------
		if coinbaseAddr != "" && normalizedAccount == coinbaseAddr {
			state.ComputedBalance.Add(state.ComputedBalance, coinbaseGasFee)
		}
		if zkvmAddr != "" && normalizedAccount == zkvmAddr {
			state.ComputedBalance.Add(state.ComputedBalance, zkvmGasFee)
		}
	}

	return state
}

// calculateGasCostFromTx computes the gas cost for a transaction.
// NOTE: DBTransaction only carries GasLimit, not GasUsed (which lives in block headers).
// This uses GasLimit as a conservative upper bound. For exact reconciliation,
// add a GasUsed field to DBTransaction populated from the transaction receipt.
func (r *Reconciliation) calculateGasCostFromTx(tx *types.DBTransaction) *big.Int {
	if tx.GasLimit == 0 {
		return big.NewInt(0)
	}

	gasLimit := new(big.Int).SetUint64(tx.GasLimit)
	var gasPrice *big.Int

	switch tx.Type {
	case 0: // Legacy
		gasPrice = tx.GasPrice
	case 1, 2: // EIP-2930, EIP-1559
		// Use max_fee as a safe upper bound for effective gas price
		gasPrice = tx.MaxFee
		if gasPrice == nil {
			gasPrice = tx.MaxPriorityFee
		}
	default:
		return big.NewInt(0)
	}

	if gasPrice == nil {
		return big.NewInt(0)
	}

	// Gas cost = gas_limit * gas_price (upper bound — see function comment)
	return new(big.Int).Mul(gasLimit, gasPrice)
}

// Close releases resources and cleans up.
func (r *Reconciliation) Close() {
	if r.SyncVars != nil {
		r.SyncVars = nil

	}
	if r.headerCache != nil {
		err := r.headerCache.Close()
		if err != nil {
			ctx, cancel := context.WithTimeout(r.SyncVars.Ctx, 3*time.Second)
			defer cancel()
			Log.Logger(namedlogger).Error(ctx, "Failed to close header cache", err)
		}
	}
}
