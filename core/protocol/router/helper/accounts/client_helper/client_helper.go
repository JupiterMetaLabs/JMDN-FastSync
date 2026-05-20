package clienthelper

import (
	"context"
	"errors"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	accountshelper "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper/accounts"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
)

type clientHelper struct {
	SyncVars *types.Syncvars
}

type AccountStorage interface {
	SetSyncVars(ctx context.Context,  nodeInfo types.Nodeinfo, wal *WAL.WAL) AccountStorage
	GetSyncVars() *types.Syncvars
	WriteAccounts(accounts []*accountspb.Account) (bool, error)
	Close()
}

func NewClientWriter() AccountStorage {
	return &clientHelper{}
}

func (clienthelper *clientHelper) SetSyncVars(ctx context.Context, nodeInfo types.Nodeinfo,wal *WAL.WAL) AccountStorage {
	if clienthelper.SyncVars == nil {
		clienthelper.SyncVars = &types.Syncvars{}
	}
	clienthelper.SyncVars.Ctx = ctx
	clienthelper.SyncVars.NodeInfo = nodeInfo
	clienthelper.SyncVars.WAL = wal
	return clienthelper
}

func (clienthelper *clientHelper) GetSyncVars() *types.Syncvars {
	return clienthelper.SyncVars
}

// WriteAccounts persists a page of accounts received from the server.
//
// WAL writes are async: the event is serialised and enqueued into the async
// buffer (no disk I/O on the hot path). The background buffer goroutine flushes
// to disk every 60 ms. The DB write happens immediately without waiting for the
// WAL flush — CreateAccount is idempotent so crash recovery via WAL replay is safe.
//
// If WAL is nil, writes directly to DB (e.g. in tests).
func (clienthelper *clientHelper) WriteAccounts(protoaccounts []*accountspb.Account) (bool, error) {
	if len(protoaccounts) == 0 {
		return false, errors.New("no accounts to write")
	}

	accounts := accountshelper.ProtoToAccounts(protoaccounts)
	if len(accounts) == 0 {
		return false, errors.New("no accounts to write")
	}

	ctx := clienthelper.SyncVars.Ctx

	// ── 1. Async WAL enqueue (non-blocking) ───────────────────────────────────
	if clienthelper.SyncVars.WAL != nil {
		event := &WAL.AccountSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Response: &accountspb.AccountSyncResponse{
				Accounts: protoaccounts,
			},
		}
		if err := clienthelper.SyncVars.WAL.AddToBufferWAL(event); err != nil {
			return false, fmt.Errorf("accountsync client: async WAL enqueue failed: %w", err)
		}
		Log.Logger(Log.Sync).Debug(ctx, "accountsync client: WAL entry enqueued async",
			ion.Int("account_count", len(protoaccounts)))
	} else {
		Log.Logger(Log.Sync).Warn(ctx, "accountsync client: WAL is nil — skipping WAL write",
			ion.Int("account_count", len(protoaccounts)))
	}

	// ── 2. DB write ───────────────────────────────────────────────────────────
	if err := clienthelper.SyncVars.NodeInfo.BlockInfo.NewAccountManager().WriteAccounts(accounts); err != nil {
		return false, fmt.Errorf("accountsync client: DB write failed: %w", err)
	}

	Log.Logger(Log.Sync).Info(ctx, "accountsync client: accounts written to DB",
		ion.Int("account_count", len(accounts)))

	// ── 3. Async checkpoint enqueue ───────────────────────────────────────────
	if clienthelper.SyncVars.WAL != nil {
		clienthelper.SyncVars.WAL.AddCheckpointToBuffer()
	}

	return true, nil
}

func (clienthelper *clientHelper) Close() {
	clienthelper.SyncVars = nil
}
