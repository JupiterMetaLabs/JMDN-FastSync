package constants

import "github.com/libp2p/go-libp2p/core/protocol"

// Protocol IDs for message and file sharing
const (
	PriorSyncProtocol protocol.ID = "/priorsync/v1"
	MerkleProtocol    protocol.ID = "/priorsync/v1/merkle"
)