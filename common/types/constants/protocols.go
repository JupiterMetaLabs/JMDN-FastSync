package constants

import "github.com/libp2p/go-libp2p/core/protocol"

// Protocol IDs for message and file sharing
const (
	AvailabilityProtocol       protocol.ID = "/fastsync/v1/availability"
	PriorSyncProtocol          protocol.ID = "/fastsync/v1/priorsync"
	MerkleProtocol             protocol.ID = "/fastsync/v1/merkle"
	HeaderSyncProtocol         protocol.ID = "/fastsync/v1/headersync"
	DataSyncProtocol           protocol.ID = "/fastsync/v1/datasync"
	PoTSProtocol               protocol.ID = "/fastsync/v1/pots"
)