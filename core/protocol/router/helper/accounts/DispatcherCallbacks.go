package accounts

import (
	"context"
	"encoding/json"
	"fmt"

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

// StreamAccountsFn is the dial-back function used to deliver one page to the client.
// The server opens a new AccountsSyncDataProtocol stream per call, sends the page,
// and reads the client's BatchAck. Each call is independent — no shared stream state.
type StreamAccountsFn func(ctx context.Context, remote types.Nodeinfo, msg *accountspb.AccountSyncServerMessage) (*accountspb.AccountSyncServerMessage, error)

// NewDispatcherCallbacks builds the types.DispatcherCallbacks for one AccountSync session.
//
// Parameters:
//   - nodeinfo       : provides BlockInfo → AccountManager → GetAccountsByNonces
//   - remote         : client's Nodeinfo used by streamAccounts to dial back per page
//   - streamAccounts : dials the client on AccountsSyncDataProtocol, sends the page, reads BatchAck
//   - auth           : session auth token embedded in every AccountSyncResponse.Phase
//
// Each page is delivered on its own short-lived stream via streamAccounts — no shared
// stream mutex needed. DB fetches (FetchAccounts) and page deliveries run fully in
// parallel across all dispatch workers.
//
// Time: O(1). Space: O(1).
func NewDispatcherCallbacks(
	nodeinfo *types.Nodeinfo,
	remote types.Nodeinfo,
	streamAccounts StreamAccountsFn,
	auth *authpb.Auth,
) types.DispatcherCallbacks {
	return types.DispatcherCallbacks{
		FetchAccounts: buildFetchAccounts(nodeinfo),
		SendPage:      buildSendPage(remote, streamAccounts, auth),
		OnPageMetrics: buildOnPageMetrics(),
		OnDeadLetter:  buildOnDeadLetter(nodeinfo, remote, streamAccounts, auth),
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

// buildSendPage returns a closure that dials the client and delivers one page.
//
// Steps:
//  1. Convert []*types.Account → proto with balance="0"
//  2. streamAccounts(ctx, remote, msg) → dials client on AccountsSyncDataProtocol,
//     sends the AccountSyncResponse, reads back the client's BatchAck
//  3. Return error if the dial/send fails or BatchAck.Ok is false
//
// No mutex needed — each call opens its own stream so workers never share state.
//
// Time: O(n) where n = len(accounts) + one network round-trip. Space: O(n).
func buildSendPage(
	remote types.Nodeinfo,
	streamAccounts StreamAccountsFn,
	auth *authpb.Auth,
) func(context.Context, uint32, []*types.Account) error {
	return func(ctx context.Context, pageIndex uint32, accounts []*types.Account) error {
		Log.Logger(Log.Sync).Info(ctx, "accountsync: sending page to client",
			ion.Int("page_index", int(pageIndex)),
			ion.Int("account_count", len(accounts)),
			ion.String("to_peer", remote.PeerID.String()),
		)

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

		ack, err := streamAccounts(ctx, remote, msg)
		if err != nil {
			return fmt.Errorf("accountsync send page %d: %w", pageIndex, err)
		}
		if ack.GetBatchAck() != nil && !ack.GetBatchAck().GetAck().GetOk() {
			return fmt.Errorf("accountsync client rejected page %d: %s", pageIndex, ack.GetBatchAck().GetAck().GetError())
		}

		Log.Logger(Log.Sync).Info(ctx, "accountsync: client ACKed page",
			ion.Int("page_index", int(pageIndex)),
			ion.Int("account_count", len(accounts)),
			ion.String("from_peer", remote.PeerID.String()),
		)
		return nil
	}
}

// ─── OnPageMetrics ───────────────────────────────────────────────────────────

// buildOnPageMetrics logs per-page delivery timings after every dispatch attempt.
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
//
// Makes one final synchronous attempt:
//  1. Re-fetches accounts from DB (transient errors may have cleared).
//  2. Calls streamAccounts — same dial-back contract as normal pages.
//     PageIndex=0 signals a recovery page to the client.
//  3. On failure: logs and abandons. No further retries.
//
// Time: O(n). Space: O(n).
func buildOnDeadLetter(
	nodeinfo *types.Nodeinfo,
	remote types.Nodeinfo,
	streamAccounts StreamAccountsFn,
	auth *authpb.Auth,
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

		ack, sendErr := streamAccounts(ctx, remote, msg)
		if sendErr != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter send failed — permanently lost",
				sendErr, ion.Int("nonce_count", len(dead.Nonces)))
			return
		}
		if ack.GetBatchAck() != nil && !ack.GetBatchAck().GetAck().GetOk() {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter ack rejected — permanently lost",
				fmt.Errorf("%s", ack.GetBatchAck().GetAck().GetError()), ion.Int("nonce_count", len(dead.Nonces)))
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
