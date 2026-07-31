package reconsillation

import (
	"context"
	"math/big"
	"testing"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/ethereum/go-ethereum/common"
)

// These tests pin the ART-identity invariant of the reconciliation producers:
// AccountUpdate.Nonce carries the account's STORED identity nonce (or 0 = "no
// identity information") and never a transaction nonce. This is exactly the
// bug class that shipped before — delta.Nonce / state.Nonce (max outgoing
// tx.Nonce) written into the identity field — and core/reconsillation had no
// tests at all, so the regression would be silent.

// fakeAccountManager implements types.AccountManager for the two compute
// functions under test. Only GetAccountBalance and
// GetTransactionsForAccountInRange are exercised; the rest panic loudly.
type fakeAccountManager struct {
	balance       *big.Int // nil = account not held (missing)
	identityNonce uint64
	txs           []types.DBTransaction
}

func (f *fakeAccountManager) GetAccountBalance(addr string) (*big.Int, uint64, error) {
	return f.balance, f.identityNonce, nil
}

func (f *fakeAccountManager) GetTransactionsForAccountInRange(addr string, from, to uint64) ([]types.DBTransaction, error) {
	return f.txs, nil
}

func (f *fakeAccountManager) GetTransactionsForAccount(string) ([]types.DBTransaction, error) {
	panic("unused in test")
}
func (f *fakeAccountManager) GetAccountByAddress(string) (*types.Account, error) {
	panic("unused in test")
}
func (f *fakeAccountManager) UpdateAccountBalance(string, *big.Int, uint64) error {
	panic("unused in test")
}
func (f *fakeAccountManager) CreateAccount(string, *big.Int, uint64) error {
	panic("unused in test")
}
func (f *fakeAccountManager) BatchUpdateAccounts([]types.AccountUpdate) error {
	panic("unused in test")
}
func (f *fakeAccountManager) WriteAccounts([]*types.Account) error {
	panic("unused in test")
}
func (f *fakeAccountManager) NewAccountNonceIterator(int) types.AccountNonceIterator {
	panic("unused in test")
}

const testAddr = "0xAaAaAA00000000000000000000000000000000aa"

// computeUpdateFromDelta must carry the STORED identity nonce, not the
// transaction-nonce bookkeeping in delta.Nonce.
func TestComputeUpdateFromDelta_CarriesStoredIdentityNotTxNonce(t *testing.T) {
	r := &Reconciliation{}
	mgr := &fakeAccountManager{balance: big.NewInt(100), identityNonce: 777_000}

	delta := &types.AccountDelta{
		BalanceDelta: big.NewInt(50),
		Nonce:        5, // max outgoing tx.Nonce — must NEVER reach identity
		TxNonce:      6,
		TxCountSent:  1,
		IsSender:     true,
	}

	u, err := r.computeUpdateFromDelta(mgr, testAddr, delta)
	if err != nil {
		t.Fatalf("computeUpdateFromDelta: %v", err)
	}
	if u.Nonce != 777_000 {
		t.Fatalf("identity nonce: got %d, want stored 777000 (tx nonce %d must not leak into identity)", u.Nonce, delta.Nonce)
	}
	if u.TxNonce != 6 || u.TxCountSent != 1 {
		t.Fatalf("tx-nonce effects must stay in TxNonce/TxCountSent: got TxNonce=%d TxCountSent=%d", u.TxNonce, u.TxCountSent)
	}
	if u.NewBalance.Cmp(big.NewInt(150)) != 0 {
		t.Fatalf("balance: got %s, want 150", u.NewBalance)
	}
	if u.IsNewAccount {
		t.Fatalf("existing account must not be IsNewAccount")
	}
}

// A missing account (nil balance) yields the 0 identity sentinel + IsNewAccount,
// never an invented identity.
func TestComputeUpdateFromDelta_MissingAccountYieldsSentinel(t *testing.T) {
	r := &Reconciliation{}
	mgr := &fakeAccountManager{balance: nil, identityNonce: 0}

	delta := &types.AccountDelta{BalanceDelta: big.NewInt(25), Nonce: 3, TxNonce: 4, TxCountSent: 1, IsSender: true}

	u, err := r.computeUpdateFromDelta(mgr, testAddr, delta)
	if err != nil {
		t.Fatalf("computeUpdateFromDelta: %v", err)
	}
	if u.Nonce != 0 {
		t.Fatalf("missing account must carry the 0 sentinel, got %d", u.Nonce)
	}
	if !u.IsNewAccount {
		t.Fatalf("missing account must be IsNewAccount")
	}
	if u.NewBalance.Cmp(big.NewInt(25)) != 0 {
		t.Fatalf("balance from zero base: got %s, want 25", u.NewBalance)
	}
}

// computeAccountUpdate (the tagged-accounts replay path) must also carry the
// stored identity — state.Nonce from the replay is tx-nonce bookkeeping.
func TestComputeAccountUpdate_CarriesStoredIdentityNotReplayNonce(t *testing.T) {
	from := common.HexToAddress(testAddr)
	to := common.HexToAddress("0xBBbbBB00000000000000000000000000000000bb")

	r := &Reconciliation{}
	r.SyncVars = &types.Syncvars{Ctx: context.Background()}

	tx := types.DBTransaction{}
	tx.From = &from
	tx.To = &to
	tx.Value = big.NewInt(10)
	tx.Nonce = 9 // outgoing tx nonce — must NOT become the identity
	tx.GasLimit = 21000
	tx.GasPrice = big.NewInt(1)
	tx.BlockNumber = 12

	mgr := &fakeAccountManager{
		balance:       big.NewInt(1_000_000),
		identityNonce: 424_242,
		txs:           []types.DBTransaction{tx},
	}

	u, err := r.computeAccountUpdate(mgr, testAddr, 10, 20)
	if err != nil {
		t.Fatalf("computeAccountUpdate: %v", err)
	}
	if u.Nonce != 424_242 {
		t.Fatalf("identity nonce: got %d, want stored 424242 (replay nonce 9 must not leak into identity)", u.Nonce)
	}
	if u.IsNewAccount {
		t.Fatalf("existing account must not be IsNewAccount")
	}
}
