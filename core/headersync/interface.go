package headersync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Headersync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) Headersync_router

	// HeaderSync is the main function that will be called by the user to sync headers
	HeaderSync(headersyncrequest *headersyncpb.HeaderSyncRequest, remotes []*types.Nodeinfo) (*datasyncpb.DataSyncRequest, error)

	// SyncConfirmation is the function that will be called by the HeaderSync function to confirm the sync
	SyncConfirmation(ctx context.Context, remotes []*types.Nodeinfo) (*taggingpb.Tag, bool, error)

	// Get the SyncVars - helpful for other modules to get the sync vars
	GetSyncVars() *types.Syncvars

	// Close the connection
	Close()
}
