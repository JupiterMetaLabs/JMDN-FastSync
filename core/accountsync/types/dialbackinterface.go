package types

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	"github.com/libp2p/go-libp2p/core/host"
)

type AccountSyncData_router interface{
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) AccountSyncData_router

	// Get the SyncVars - helpful for other modules to get the sync vars
	GetSyncVars() *types.Syncvars

	// This will start listening for the account sync data from the server. 
	// This is used to handle the account sync data from the server.
	ProcessAccountsPage(ctx context.Context, resp *accountspb.AccountSyncServerMessage) error

	// Close the connection
	Close()
}