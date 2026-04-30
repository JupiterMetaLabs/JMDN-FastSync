package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"google.golang.org/protobuf/types/known/structpb"
)

// NewDispatcherCallbacks builds the types.DispatcherCallbacks for one AccountSync session.
//
// Parameters:
//   - nodeinfo  : provides BlockInfo → AccountManager → GetAccountsByNonces
//   - writeMsg  : mutex-protected stream writer (same stream as the ART upload)
//   - readAck   : reads one Ack from the client on the same stream after a page write
//   - auth      : session auth token embedded in every AccountSyncResponse.Phase
//
// A shared pageMu is created here and captured by both SendPage and OnDeadLetter.
// It serialises the write→Ack round-trip so concurrent workers never read each
// other's Acks.
//
// Time: O(1). Space: O(1).
func NewDispatcherCallbacks(
	nodeinfo *types.Nodeinfo,
	writeMsg func(*accountspb.AccountSyncServerMessage) error,
	readAck func() (*ackpb.Ack, error),
	auth *authpb.Auth,
) types.DispatcherCallbacks {
	// pageMu serialises write+Ack across concurrent dispatch workers.
	// Without it: worker A sends page 3, worker B sends page 7, client Acks
	// page 3 — worker B reads it and wrongly thinks page 7 was accepted.
	// With it: one goroutine holds write→readAck→return atomically.
	// DB fetches (FetchAccounts) still run in parallel — only the network
	// send+Ack phase is serialised.
	var pageMu sync.Mutex

	return types.DispatcherCallbacks{
		FetchAccounts: buildFetchAccounts(nodeinfo),
		SendPage:      buildSendPage(writeMsg, readAck, auth, &pageMu),
		OnPageMetrics: buildOnPageMetrics(),
		OnDeadLetter:  buildOnDeadLetter(nodeinfo, writeMsg, readAck, auth, &pageMu),
	}
}

// ─── FetchAccounts ────────────────────────────────────────────────────────────

// buildFetchAccounts returns a closure that batch-fetches full account rows
// for up to NoncePageSize nonces via GetAccountsByNonces (single DB query).
//
// Time: O(n) where n = len(nonces). Space: O(n).
func buildFetchAccounts(nodeinfo *types.Nodeinfo) func(context.Context, []uint64) ([]*types.Account, error) {
	return func(ctx context.Context, nonces []uint64) ([]*types.Account, error) {
		return nodeinfo.BlockInfo.NewAccountManager().NewAccountNonceIterator(1).GetAccountsByNonces(nonces)
	}
}

// ─── SendPage ────────────────────────────────────────────────────────────────

// buildSendPage returns a closure that sends one page and reads the client Ack.
//
// Steps (all under pageMu):
//  1. Convert []*types.Account → proto with balance="0"
//  2. writeMsg(AccountSyncResponse) → sends page on the existing stream
//  3. readAck() → blocks until client sends Ack back on the same stream
//  4. Return error if write fails, read fails, or Ack.Ok is false
//
// Time: O(n) where n = len(accounts) + one network round-trip. Space: O(n).
func buildSendPage(
	writeMsg func(*accountspb.AccountSyncServerMessage) error,
	readAck func() (*ackpb.Ack, error),
	auth *authpb.Auth,
	pageMu *sync.Mutex,
) func(context.Context, uint32, []*types.Account) error {
	return func(ctx context.Context, pageIndex uint32, accounts []*types.Account) error {
		msg := &accountspb.AccountSyncServerMessage{
			Payload: &accountspb.AccountSyncServerMessage_Response{
				Response: &accountspb.AccountSyncResponse{
					Accounts:  ToProtoAccounts(accounts),
					PageIndex: pageIndex,
					Ack:       &ackpb.Ack{Ok: true},
					Phase: &phasepb.Phase{
						PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
						SuccessivePhase: constants.ACCOUNTS_SYNC_RESPONSE,
						Success:         true,
						Auth:            auth,
					},
				},
			},
		}

		pageMu.Lock()
		defer pageMu.Unlock()

		if err := writeMsg(msg); err != nil {
			return fmt.Errorf("accountsync send page %d: %w", pageIndex, err)
		}
		ack, err := readAck()
		if err != nil {
			return fmt.Errorf("accountsync read ack page %d: %w", pageIndex, err)
		}
		if !ack.Ok {
			return fmt.Errorf("accountsync client rejected page %d: %s", pageIndex, ack.Error)
		}
		return nil
	}
}

// ─── OnPageMetrics ───────────────────────────────────────────────────────────

// buildOnPageMetrics logs per-page delivery timings after every dispatch attempt.
// Runs off the hotpath — never blocks a worker.
//
// Time: O(1). Space: O(1).
func buildOnPageMetrics() func(context.Context, types.DispatchPageMetrics) {
	return func(ctx context.Context, m types.DispatchPageMetrics) {
		if m.Success {
			Log.Logger(Log.Sync).Info(ctx, "accountsync: page delivered",
				ion.Int("page_index", int(m.PageIndex)),
				ion.Int("nonce_count", m.NonceCount),
				ion.Int64("db_fetch_ms", m.DBFetchMS),
				ion.Int64("send_ms", m.SendMS),
				ion.Int("retries", m.Retries),
			)
		} else {
			Log.Logger(Log.Sync).Warn(ctx, "accountsync: page delivery failed",
				ion.Int("page_index", int(m.PageIndex)),
				ion.Int("nonce_count", m.NonceCount),
				ion.Int64("db_fetch_ms", m.DBFetchMS),
				ion.Int64("send_ms", m.SendMS),
				ion.Int("retries", m.Retries),
			)
		}
	}
}

// ─── OnDeadLetter ────────────────────────────────────────────────────────────

// buildOnDeadLetter fires when a page exhausts all dispatcher retries.
// Runs away from the hotpath — workers have moved on when this is called.
//
// Makes one final synchronous attempt:
//  1. Re-fetches accounts from DB (transient errors may have cleared).
//  2. Sends via writeMsg + reads Ack under pageMu — same contract as normal pages.
//     page_index=0 signals a recovery page to the client.
//  3. On failure: logs and abandons. No further retries.
//
// Time: O(n). Space: O(n).
func buildOnDeadLetter(
	nodeinfo *types.Nodeinfo,
	writeMsg func(*accountspb.AccountSyncServerMessage) error,
	readAck func() (*ackpb.Ack, error),
	auth *authpb.Auth,
	pageMu *sync.Mutex,
) func(context.Context, types.DeadLetterPage) {
	return func(ctx context.Context, dead types.DeadLetterPage) {
		Log.Logger(Log.Sync).Warn(ctx, "accountsync: dead-lettered — final recovery attempt",
			ion.Int("nonce_count", len(dead.Nonces)),
			ion.Int("retry_count", dead.RetryCount),
			ion.String("failure_hint", dead.FailureHint),
		)

		accounts, err := buildFetchAccounts(nodeinfo)(ctx, dead.Nonces)
		if err != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter fetch failed — permanently lost",
				err, ion.Int("nonce_count", len(dead.Nonces)))
			return
		}

		msg := &accountspb.AccountSyncServerMessage{
			Payload: &accountspb.AccountSyncServerMessage_Response{
				Response: &accountspb.AccountSyncResponse{
					Accounts:  ToProtoAccounts(accounts),
					PageIndex: 0,
					Ack:       &ackpb.Ack{Ok: true},
					Phase: &phasepb.Phase{
						PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
						SuccessivePhase: constants.ACCOUNTS_SYNC_RESPONSE,
						Success:         true,
						Auth:            auth,
					},
				},
			},
		}

		pageMu.Lock()
		defer pageMu.Unlock()

		if sendErr := writeMsg(msg); sendErr != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter send failed — permanently lost",
				sendErr, ion.Int("nonce_count", len(dead.Nonces)))
			return
		}
		ack, ackErr := readAck()
		if ackErr != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter ack read failed — permanently lost",
				ackErr, ion.Int("nonce_count", len(dead.Nonces)))
			return
		}
		if !ack.Ok {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter ack rejected — permanently lost",
				fmt.Errorf("%s", ack.Error), ion.Int("nonce_count", len(dead.Nonces)))
			return
		}
		Log.Logger(Log.Sync).Info(ctx, "accountsync: dead-letter recovery succeeded",
			ion.Int("nonce_count", len(dead.Nonces)))
	}
}

// ─── Proto conversion ─────────────────────────────────────────────────────────

// ToProtoAccounts converts []*types.Account → []*accountspb.Account.
// Balance is always "0" — Reconciliation fills actual balances post-DataSync.
// Nil entries are silently skipped.
//
// Time: O(n). Space: O(n).
func ToProtoAccounts(accounts []*types.Account) []*accountspb.Account {
	out := make([]*accountspb.Account, 0, len(accounts))
	for _, acc := range accounts {
		if acc == nil {
			continue
		}
		proto := &accountspb.Account{
			DidAddress:  acc.DIDAddress,
			Address:     acc.Address.Bytes(),
			Balance:     "0",
			Nonce:       acc.Nonce,
			AccountType: acc.AccountType,
			CreatedAt:   acc.CreatedAt,
			UpdatedAt:   acc.UpdatedAt,
		}
		if len(acc.Metadata) > 0 {
			if b, err := json.Marshal(acc.Metadata); err == nil {
				s := &structpb.Struct{}
				if err := s.UnmarshalJSON(b); err == nil {
					proto.Metadata = s
				}
			}
		}
		out = append(out, proto)
	}
	return out
}
