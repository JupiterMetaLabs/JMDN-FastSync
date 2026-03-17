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
	if r.headerCache == nil {
		return nil, false
	}

	block, exist := r.headerCache.Get(blockNumber)
	if exist {
		return block, true
	}

	// Cache miss — fetch from DB
	if r.SyncVars == nil || r.SyncVars.NodeInfo.BlockInfo == nil {
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
		return nil, false
	}

	header := headers[0]
	r.headerCache.Put(blockNumber, header)

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

	// Log every 10% of total accounts
	logInterval := numAccounts / 10
	if logInterval == 0 {
		logInterval = 1
	}
	collected := 0

	for res := range resultCh {
		if res.err != nil {
			failedAccounts = append(failedAccounts, res.addr)
			computeErrs = append(computeErrs, res.err)
		} else {
			updates = append(updates, res.update)
		}
		collected++
		if collected%logInterval == 0 {
			Log.Logger(namedlogger).Info(ctx, "Phase 1 progress — computing account states",
				ion.Int("collected", collected),
				ion.Int("total", numAccounts),
				ion.Int("failed_so_far", len(computeErrs)))
		}
	}

	if len(computeErrs) > 0 {
		Log.Logger(namedlogger).Warn(ctx, "Some accounts failed during computation phase — aborting commit",
			ion.Int("failed_count", len(computeErrs)),
			ion.Int("succeeded_count", len(updates)))
		return 0, failedAccounts, fmt.Errorf("computation phase failed for %d accounts, no DB changes were made: %v",
			len(computeErrs), computeErrs)
	}

	Log.Logger(namedlogger).Info(ctx, "Phase 1 complete — all account states ready",
		ion.Int("accounts_ready", len(updates)))

	// ----------------------------------------------------------------
	// Phase 2: Write all planned changes to WAL as a single batch event.
	// Writing one ReconciliationBatchEvent instead of N individual events
	// avoids N mutex acquisitions, N JSON marshals, and N*BatchSize disk
	// writes — critical when account count is in the hundreds of thousands.
	// ----------------------------------------------------------------
	if r.SyncVars.WAL != nil {
		walStart := time.Now()
		Log.Logger(namedlogger).Info(ctx, "Phase 2 starting — writing WAL batch event",
			ion.Int("accounts", len(updates)))

		entries := make([]WAL.ReconciliationBatchEntry, len(updates))
		for i, u := range updates {
			entries[i] = WAL.ReconciliationBatchEntry{
				AccountAddress: u.Address,
				NewBalance:     u.NewBalance.String(),
				Nonce:          u.Nonce,
			}
		}
		batchEvent := &WAL.ReconciliationBatchEvent{
			Accounts:  entries,
			Timestamp: time.Now().Unix(),
		}
		if _, err := r.SyncVars.WAL.WriteEvent(batchEvent); err != nil {
			return 0, nil, fmt.Errorf("WAL batch write failed — aborting commit: %w", err)
		}
		if err := r.SyncVars.WAL.Flush(); err != nil {
			return 0, nil, fmt.Errorf("WAL flush failed — aborting commit: %w", err)
		}
		Log.Logger(namedlogger).Info(ctx, "Phase 2 complete — WAL flushed",
			ion.Int("accounts_in_batch", len(updates)),
			ion.String("duration", time.Since(walStart).String()))
	}

	// ----------------------------------------------------------------
	// Phase 3: Atomic DB commit — all or none
	// ----------------------------------------------------------------
	dbStart := time.Now()
	Log.Logger(namedlogger).Info(ctx, "Phase 3 starting — atomic DB commit",
		ion.Int("accounts_to_commit", len(updates)))

	if err := accountManager.BatchUpdateAccounts(updates); err != nil {
		return 0, nil, fmt.Errorf("atomic DB commit failed — no accounts were updated: %w", err)
	}

	Log.Logger(namedlogger).Info(ctx, "Phase 3 complete — reconciliation committed",
		ion.Int("accounts_committed", len(updates)),
		ion.String("duration", time.Since(dbStart).String()))

	return len(updates), nil, nil
}

// computeAccountUpdate reads all transactions for one account, replays them to get
// the new balance/nonce, and returns a ready-to-commit AccountUpdate.
// This is a read-only operation — it does not touch the DB.
func (r *Reconciliation) computeAccountUpdate(accountManager types.AccountManager, accountAddress string) (types.AccountUpdate, error) {
	// Normalize address to 0x-prefixed lowercase so calculateAccountState comparisons
	// against tx.From.Hex() / tx.To.Hex() (which always carry the 0x prefix) are correct.
	if !strings.HasPrefix(accountAddress, "0x") {
		accountAddress = "0x" + accountAddress
	}

	transactions, err := accountManager.GetTransactionsForAccount(accountAddress)
	if err != nil {
		return types.AccountUpdate{}, fmt.Errorf("failed to get transactions for account %s: %w", accountAddress, err)
	}

	state := r.calculateAccountState(accountAddress, transactions)

	currentBalance, _, err := accountManager.GetAccountBalance(accountAddress)
	if err != nil {
		return types.AccountUpdate{}, fmt.Errorf("failed to get current balance for account %s: %w", accountAddress, err)
	}

	// GetAccountBalance returns nil when the account does not exist in the DB.
	// A zero balance on an existing account must not be confused with "not found".
	isNewAccount := currentBalance == nil
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
		IsNewAccount: isNewAccount,
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
	if r.headerCache != nil {
		if err := r.headerCache.Close(); err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if r.SyncVars != nil {
				ctx = r.SyncVars.Ctx
			}
			Log.Logger(namedlogger).Error(ctx, "Failed to close header cache", err)
		}
		r.headerCache = nil
	}
	r.SyncVars = nil
}
