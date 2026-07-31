package constants

import "github.com/libp2p/go-libp2p/core/protocol"

// Protocol IDs for message and file sharing
const (
	AvailabilityProtocol      protocol.ID = "/fastsync/v1/availability"
	PriorSyncProtocol         protocol.ID = "/fastsync/v1/priorsync"
	MerkleProtocol            protocol.ID = "/fastsync/v1/merkle"
	HeaderSyncProtocol        protocol.ID = "/fastsync/v1/headersync"
	DataSyncProtocol          protocol.ID = "/fastsync/v1/datasync"
	AccountsSyncProtocol      protocol.ID = "/fastsync/v1/accountssync"
	AccountsSyncDataProtocol  protocol.ID = "/fastsync/v1/accountssync/data"
	AccountsSyncFetchProtocol protocol.ID = "/fastsync/v1/accountssync/fetch"
	PoTSProtocol              protocol.ID = "/fastsync/v1/pots"

	// HeaderSyncProtocolV2 carries the HeaderSyncStreamMessage ENVELOPE
	// (heartbeats + wrapped response). The envelope changed the wire format, and
	// keeping it on the v1 ID made mixed versions misparse SILENTLY (a real
	// header payload decodes as a heartbeat, both directions return nil error —
	// review finding FS1). v1 therefore stays the pre-envelope bare
	// HeaderSyncResponse wire forever; envelope traffic negotiates v2. Servers
	// register BOTH; clients offer [v2, v1] and branch on the negotiated ID.
	HeaderSyncProtocolV2 protocol.ID = "/fastsync/v2/headersync"
)

// This protocol is for the pubsub implementation
const (
	BlocksPUBSUB protocol.ID = "/fastsync/v1/pubsub/blocks"
)
