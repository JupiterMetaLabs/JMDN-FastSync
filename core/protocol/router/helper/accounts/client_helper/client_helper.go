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
// If WAL is configured:
//  1. Write AccountSyncEvent to WAL (crash-safe record before DB touch)
//  2. Flush WAL to disk
//  3. Write accounts to DB
//  4. Create WAL checkpoint (mark these entries as applied)
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

	// ── 1. WAL write ──────────────────────────────────────────────────────────
	if clienthelper.SyncVars.WAL != nil {
		event := &WAL.AccountSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Response: &accountspb.AccountSyncResponse{
				Accounts: protoaccounts,
			},
		}
		lsn, err := clienthelper.SyncVars.WAL.WriteEvent(event)
		if err != nil {
			return false, fmt.Errorf("accountsync client: WAL write failed: %w", err)
		}
		Log.Logger(Log.Sync).Debug(ctx, "accountsync client: WAL event written",
			ion.Int64("lsn", int64(lsn)),
			ion.Int("account_count", len(protoaccounts)))

		// ── 2. WAL flush ──────────────────────────────────────────────────────
		if err := clienthelper.SyncVars.WAL.Flush(); err != nil {
			return false, fmt.Errorf("accountsync client: WAL flush failed: %w", err)
		}
		Log.Logger(Log.Sync).Debug(ctx, "accountsync client: WAL flushed",
			ion.Int64("last_flushed_lsn", int64(clienthelper.SyncVars.WAL.GetLastFlushedLSN())))
	} else {
		Log.Logger(Log.Sync).Warn(ctx, "accountsync client: WAL is nil — skipping WAL write",
			ion.Int("account_count", len(protoaccounts)))
	}

	// ── 3. DB write ───────────────────────────────────────────────────────────
	if err := clienthelper.SyncVars.NodeInfo.BlockInfo.NewAccountManager().WriteAccounts(accounts); err != nil {
		return false, fmt.Errorf("accountsync client: DB write failed: %w", err)
	}

	Log.Logger(Log.Sync).Info(ctx, "accountsync client: accounts written to DB",
		ion.Int("account_count", len(accounts)))

	// ── 4. WAL checkpoint ─────────────────────────────────────────────────────
	if clienthelper.SyncVars.WAL != nil {
		if _, err := clienthelper.SyncVars.WAL.CreateCheckpoint(); err != nil {
			Log.Logger(Log.Sync).Warn(ctx, "accountsync client: WAL checkpoint failed after DB write",
				ion.Err(err))
		}
	}

	return true, nil
}

func (clienthelper *clientHelper) Close() {
	clienthelper.SyncVars = nil
}
