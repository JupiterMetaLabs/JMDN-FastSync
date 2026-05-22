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

// OpenStreamFn dials the client on AccountsSyncDataProtocol and returns an open
// persistent stream. Caller owns the stream lifetime.
type OpenStreamFn func(ctx context.Context, peerNode types.Nodeinfo) (types.AccountSyncStream, error)

// SendPageOnStreamFn writes one AccountSyncServerMessage to an open stream and
// reads back the client's ACK. Does NOT open or close the stream.
type SendPageOnStreamFn func(ctx context.Context, stream types.AccountSyncStream, msg *accountspb.AccountSyncServerMessage) (*accountspb.AccountSyncServerMessage, error)

// NewDispatcherCallbacks builds the types.DispatcherCallbacks for one AccountSync session.
//
// Parameters:
//   - nodeinfo    : provides BlockInfo → AccountManager pool for DB fetches
//   - remote      : client's Nodeinfo (bound into OpenStream closure)
//   - openFn      : comm.OpenAccountsDataStream — dials client, returns open stream
//   - sendFn      : comm.SendAccountPageOnStream — write+read on existing stream
//   - numWorkers  : number of AccountManagers to pre-create in the fetch pool
//   - auth        : session auth token embedded in every AccountSyncResponse.Phase
//
// Each dispatch worker opens ONE stream at startup and reuses it for every page.
// DB fetches use a pool of numWorkers AccountManagers (one per worker) to avoid
// per-page session creation.
func NewDispatcherCallbacks(
	nodeinfo *types.Nodeinfo,
	remote types.Nodeinfo,
	openFn OpenStreamFn,
	sendFn SendPageOnStreamFn,
	numWorkers int,
	auth *authpb.Auth,
) types.DispatcherCallbacks {
	return types.DispatcherCallbacks{
		FetchAccounts:    buildFetchAccountsWithPool(nodeinfo, numWorkers),
		OpenStream:       buildOpenStream(remote, openFn),
		SendPageOnStream: buildSendPageOnStream(sendFn, auth),
		OnPageMetrics:    buildOnPageMetrics(),
		OnDeadLetter:     buildOnDeadLetter(nodeinfo, remote, openFn, sendFn, auth),
	}
}

// ─── FetchAccounts ────────────────────────────────────────────────────────────

// buildFetchAccountsWithPool pre-creates numWorkers AccountManagers and hands
// them out one-at-a-time via a buffered channel pool.
//
// Each borrow creates an AccountNonceIterator for the nonce set, calls
// GetAccountsByNonces, closes the iterator, and returns the manager to the pool.
// No new AccountManager is created on the hot path — only numWorkers total are
// ever created for the lifetime of this session.
func buildFetchAccountsWithPool(nodeinfo *types.Nodeinfo, numWorkers int) func(context.Context, []uint64) ([]*types.Account, error) {
	pool := make(chan types.AccountManager, numWorkers)
	for i := 0; i < numWorkers; i++ {
		pool <- nodeinfo.BlockInfo.NewAccountManager()
	}
	return func(ctx context.Context, nonces []uint64) ([]*types.Account, error) {
		var mgr types.AccountManager
		select {
		case mgr = <-pool:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { pool <- mgr }()

		iter := mgr.NewAccountNonceIterator(len(nonces))
		defer iter.Close()
		return iter.GetAccountsByNonces(nonces)
	}
}

// ─── OpenStream ───────────────────────────────────────────────────────────────

// buildOpenStream returns a closure that opens a persistent stream to the client.
// Each dispatch worker calls this once at startup.
func buildOpenStream(
	remote types.Nodeinfo,
	openFn OpenStreamFn,
) func(context.Context) (types.AccountSyncStream, error) {
	return func(ctx context.Context) (types.AccountSyncStream, error) {
		return openFn(ctx, remote)
	}
}

// ─── SendPageOnStream ─────────────────────────────────────────────────────────

// buildSendPageOnStream returns a closure that serialises accounts to proto,
// writes the AccountSyncResponse on an existing stream, and reads the ACK.
func buildSendPageOnStream(
	sendFn SendPageOnStreamFn,
	auth *authpb.Auth,
) func(context.Context, types.AccountSyncStream, uint32, []*types.Account) error {
	return func(ctx context.Context, stream types.AccountSyncStream, pageIndex uint32, accounts []*types.Account) error {
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

		ack, err := sendFn(ctx, stream, msg)
		if err != nil {
			return fmt.Errorf("accountsync send page %d: %w", pageIndex, err)
		}
		if ack == nil {
			return fmt.Errorf("accountsync send page %d: nil ack with nil error", pageIndex)
		}
		if ack.GetBatchAck() == nil {
			return fmt.Errorf("accountsync send page %d: unexpected response type %T", pageIndex, ack.Payload)
		}
		if !ack.GetBatchAck().GetAck().GetOk() {
			return fmt.Errorf("accountsync client rejected page %d: %s", pageIndex, ack.GetBatchAck().GetAck().GetError())
		}

		Log.Logger(Log.Sync).Info(ctx, "accountsync: client ACKed page",
			ion.Int("page_index", int(pageIndex)),
			ion.Int("account_count", len(accounts)),
		)
		return nil
	}
}

// ─── OnPageMetrics ───────────────────────────────────────────────────────────

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
// Opens a one-off recovery stream, re-fetches accounts, makes one final send.
func buildOnDeadLetter(
	nodeinfo *types.Nodeinfo,
	remote types.Nodeinfo,
	openFn OpenStreamFn,
	sendFn SendPageOnStreamFn,
	auth *authpb.Auth,
) func(context.Context, types.DeadLetterPage) {
	send := buildSendPageOnStream(sendFn, auth)
	open := buildOpenStream(remote, openFn)

	return func(ctx context.Context, dead types.DeadLetterPage) {
		Log.Logger(Log.Sync).Warn(ctx, "accountsync: dead-lettered — final recovery attempt",
			ion.Int("nonce_count", len(dead.Nonces)),
			ion.Int("retry_count", dead.RetryCount),
			ion.String("failure_hint", dead.FailureHint),
		)

		iter := nodeinfo.BlockInfo.NewAccountManager().NewAccountNonceIterator(len(dead.Nonces))
		defer iter.Close()
		accounts, err := iter.GetAccountsByNonces(dead.Nonces)
		if err != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter fetch failed — permanently lost",
				err, ion.Int("nonce_count", len(dead.Nonces)))
			return
		}

		stream, err := open(ctx)
		if err != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter stream open failed — permanently lost",
				err, ion.Int("nonce_count", len(dead.Nonces)))
			return
		}
		defer stream.Close()

		if err := send(ctx, stream, 0, accounts); err != nil {
			Log.Logger(Log.Sync).Error(ctx, "accountsync: dead-letter send failed — permanently lost",
				err, ion.Int("nonce_count", len(dead.Nonces)))
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
