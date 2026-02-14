package protocol

import (
	"context"

	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Priorsync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocolversion uint16, Nodeinfo types.Nodeinfo) Priorsync_router

	// HandlePriorSync processes an incoming PriorSync request from a peer
	HandlePriorSync(node host.Host) error

	// SendPriorSync sends a PriorSync request to a specific peer and returns the response
	SendPriorSync(ctx context.Context, merkle *merklepb.MerkleSnapshot, peer types.Nodeinfo, data types.PriorSyncMessage) (*types.PriorSyncMessage, error)

	// This is to send the request for merkle tree for the given range.
	SendMerkleRequest(ctx context.Context, peerNode types.Nodeinfo, req *merklepb.MerkleRequestMessage) (*merklepb.MerkleMessage, error)

	// Close the connection
	Close()
}
