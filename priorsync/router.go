package protocol

import (
	"context"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/JupiterMetaLabs/JMDN-FastSync/types"
)

type Priorsync_router interface {
	// HandlePriorSync processes an incoming PriorSync request from a peer
	HandlePriorSync(ctx context.Context, node host.Host) error

	// SendPriorSync sends a PriorSync request to a specific peer
	SendPriorSync(ctx context.Context, peer types.Nodeinfo, data types.PriorSync, node host.Host) error
}
