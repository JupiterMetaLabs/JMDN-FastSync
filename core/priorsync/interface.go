package priorsync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Priorsync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocolversion uint16, Nodeinfo types.Nodeinfo) Priorsync_router

	// SetupNetworkHandlers processes an incoming PriorSync request from a peer
	SetupNetworkHandlers(node host.Host) error

	// Close the connection
	Close()
}
