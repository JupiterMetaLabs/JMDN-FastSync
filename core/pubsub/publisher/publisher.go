package publisher

import (
	"context"
	"sync"
	"time"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	pubsubpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pubsub"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

const namedlogger = "log:pubsub:publisher"

// Publisher is a stateless, fire-and-forget 1:N block publisher.
// It holds open libp2p streams for all accepted subscribers and pushes each
// incoming block directly — no retry, no ACK, no per-subscriber state.
type Publisher struct {
	mu      sync.Mutex
	streams []network.Stream

	node   host.Host
	ctx    context.Context
	cancel context.CancelFunc
}

var (
	publisherInstance *Publisher
	publisherOnce     sync.Once
)

// NewPublisher returns a zero-value Publisher ready for SetPublisher.
func NewPublisher() *Publisher {
	publisherOnce.Do(func() {
		publisherInstance = &Publisher{}
	})
	return publisherInstance
}

// SetPublisher initialises the publisher with the given context and host.
// Must be called before StartPublisher.
func (p *Publisher) SetPublisher(ctx context.Context, node host.Host) Publisher_router {
	ctx, cancel := context.WithCancel(ctx)
	p.ctx = ctx
	p.cancel = cancel
	p.node = node
	return p
}

// StartPublisher starts the background goroutine that watches fastsync
// availability. When availability drops to false, all open streams receive
// PubSubDone and are closed automatically.
func (p *Publisher) StartPublisher() Publisher_router {
	go p.watchAvailability()
	Log.Logger(namedlogger).Info(p.ctx, "pubsub publisher started",
		ion.String("peer", p.node.ID().String()))
	return p
}

// AddStream hands an already-accepted subscriber stream to the publisher.
// The publisher keeps the stream open and manages its lifecycle from here.
func (p *Publisher) AddStream(str network.Stream) {
	p.mu.Lock()
	p.streams = append(p.streams, str)
	p.mu.Unlock()

	Log.Logger(namedlogger).Info(p.ctx, "pubsub subscriber added",
		ion.String("peer", str.Conn().RemotePeer().String()))
}

// Publish sends block to every currently subscribed stream.
// Any stream that fails a write is silently dropped — no retry.
func (p *Publisher) Publish(block *blockpb.ZKBlock) {
	msg := &pubsubpb.BlockPubSubMessage{
		Payload: &pubsubpb.BlockPubSubMessage_Block{Block: block},
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	alive := p.streams[:0]
	for _, s := range p.streams {
		_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := pbstream.WriteDelimited(s, msg); err != nil {
			s.Reset()
			continue
		}
		alive = append(alive, s)
	}
	p.streams = alive
}

// Close cancels the publisher context which triggers watchAvailability to
// send PubSubDone to all streams and close them.
func (p *Publisher) Close() {
	if p.cancel != nil {
		p.cancel()
	}
}

// watchAvailability polls fastsync availability every 100 ms.
// When availability drops to false (or the context is cancelled) it sends
// PubSubDone to every open stream and closes them.
func (p *Publisher) watchAvailability() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			p.terminateAll("publisher closed")
			return
		case <-ticker.C:
			if !availability.FastsyncReady().AmIAvailable() {
				Log.Logger(namedlogger).Info(p.ctx,
					"fastsync no longer available, terminating pubsub streams")
				p.terminateAll("fastsync no longer available")
			}
		}
	}
}

// terminateAll sends PubSubDone to all open streams, closes them, and clears
// the subscriber list.
func (p *Publisher) terminateAll(reason string) {
	done := &pubsubpb.BlockPubSubMessage{
		Payload: &pubsubpb.BlockPubSubMessage_Done{
			Done: &pubsubpb.PubSubDone{Reason: reason},
		},
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, s := range p.streams {
		_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = pbstream.WriteDelimited(s, done)
		s.Close()
	}
	p.streams = nil
}

// ensure *Publisher satisfies Publisher_router at compile time.
var _ Publisher_router = (*Publisher)(nil)
