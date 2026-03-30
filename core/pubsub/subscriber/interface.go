package subscriber

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	potsiface "github.com/JupiterMetaLabs/JMDN-FastSync/core/pots"
	"github.com/libp2p/go-libp2p/core/host"
)

type SubscriberConfig struct {
	isConnected bool
	LocalNode  host.Host
	NodeInfo   types.Nodeinfo
	RemoteNode host.Host
	Topic      string
	WAL        potsiface.PoTS_WAL
	UUID       string // auth token obtained from HandleAvailability
}

// Subscriber_router is the client-side pubsub interface.
type Subscriber_router interface {
	// Set Subscriber
	SetSubscriber(ctx context.Context, localNode host.Host, NodeInfo types.Nodeinfo, remotenode host.Host, topic string, WAL potsiface.PoTS_WAL, uuid string)

	// Subscribe opens a stream to the server, receives live blocks for the
	// duration of fastsync, and writes each block to the PoTS WAL.
	// Returns when the server sends PubSubDone, the context is cancelled,
	// or an unrecoverable read error occurs.
	Subscribe(ctx context.Context) error

	// EndSubscription closes the subscription stream and releases resources.
	EndSubscription()
}
