package priorsync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Priorsync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocolversion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) Priorsync_router

	// PriorSync is the main function that will be called by the user to do priorsync operation
	PriorSync(local_start, local_end, remote_start, remote_end uint64, remote *types.Nodeinfo, auth_req *authpb.Auth) (*priorsyncpb.PriorSyncMessage, error)

	// SetupNetworkHandlers processes an incoming PriorSync request from a peer
	SetupNetworkHandlers(debug bool) error

	// Get the SyncVars - helpful for other modules to get the sync vars
	GetSyncVars() *types.Syncvars

	// Close the connection
	Close()
}
