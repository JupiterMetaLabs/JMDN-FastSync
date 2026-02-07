package protocol

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
)

type Priorsync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocol protocol.ID, protocolversion uint16, Nodeinfo types.Nodeinfo) Priorsync_router

	// HandlePriorSync processes an incoming PriorSync request from a peer
	HandlePriorSync(node host.Host) error

	// SendPriorSync sends a PriorSync request to a specific peer and returns the response
	SendPriorSync(merkle *merkletree.MerkleTreeSnapshot,peer types.Nodeinfo, data types.PriorSyncMessage) (*types.PriorSyncMessage, error)

	// Close the connection
	Close()
}
