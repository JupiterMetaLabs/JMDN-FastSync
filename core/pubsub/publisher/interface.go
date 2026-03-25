package publisher

import (
	"context"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

// Publisher_router is the server-side pubsub interface.
// The publisher is stateless and fire-and-forget: it pushes live blocks 1:N
// to all connected subscribers with no retry, no ACK, and no per-client state.
// Missed blocks are recovered later via PoTS sync.
type Publisher_router interface {
	// SetPublisher initialises the publisher with the given context and host.
	// Must be called before StartPublisher. Returns self for chaining.
	SetPublisher(ctx context.Context, node host.Host) Publisher_router

	// StartPublisher starts the background goroutine that watches fastsync
	// availability.  When availability drops to false, all open streams receive
	// PubSubDone and are closed automatically.
	StartPublisher() Publisher_router

	// AddStream hands an already-accepted subscriber stream to the publisher.
	// Called by the Datarouter after sending SubscribeResponse{Accepted: true}.
	// The publisher keeps the stream open and manages its lifecycle from here.
	AddStream(str network.Stream)

	// Publish sends block to every currently subscribed stream.
	// Failed writes silently drop the corresponding stream — no retry.
	Publish(block *blockpb.ZKBlock)

	// Close cancels the publisher context, sends PubSubDone to all streams,
	// and releases all resources.
	Close()
}
