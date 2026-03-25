package subscriber

import (
	"bufio"
	"context"
	"fmt"
	"time"

	pubsubpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pubsub"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	potsiface "github.com/JupiterMetaLabs/JMDN-FastSync/core/pots"
	"github.com/JupiterMetaLabs/JMDN-FastSync/helper/Blocks"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const namedlogger = "log:pubsub:subscriber"

// Subscriber connects to the publisher server, receives live blocks, and
// writes them to the PoTS WAL for later reconciliation.
type Subscriber struct {
	config SubscriberConfig
	str    network.Stream
	cancel context.CancelFunc
}

// NewSubscriber returns a zero-value Subscriber ready for SetSubscriber.
func NewSubscriber() *Subscriber {
	return &Subscriber{}
}

// SetSubscriber stores the configuration needed for Subscribe.
// topic should be the protocol ID string (e.g. constants.BlocksPUBSUB).
func (s *Subscriber) SetSubscriber(
	ctx context.Context,
	localNode host.Host,
	nodeInfo types.Nodeinfo,
	remoteNode host.Host,
	topic string,
	wal potsiface.PoTS_WAL,
) {
	s.config = SubscriberConfig{
		LocalNode:  localNode,
		NodeInfo:   nodeInfo,
		RemoteNode: remoteNode,
		Topic:      topic,
		WAL:        wal,
	}
}

// Subscribe opens a stream to the publisher server, performs the handshake,
// then enters a read loop writing received blocks to the PoTS WAL.
//
// Returns nil when the server sends PubSubDone (normal termination).
// Returns a non-nil error on handshake failure or unrecoverable stream error.
// Blocks until the server closes the stream or the context is cancelled.
func (s *Subscriber) Subscribe(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// ── Connect to server ────────────────────────────────────────────────
	addrInfo := peer.AddrInfo{
		ID:    s.config.RemoteNode.ID(),
		Addrs: s.config.RemoteNode.Addrs(),
	}
	if err := s.config.LocalNode.Connect(ctx, addrInfo); err != nil {
		cancel()
		return fmt.Errorf("pubsub subscribe: connect to server: %w", err)
	}

	str, err := s.config.LocalNode.NewStream(ctx, s.config.RemoteNode.ID(), protocol.ID(s.config.Topic))
	if err != nil {
		cancel()
		return fmt.Errorf("pubsub subscribe: open stream: %w", err)
	}
	s.str = str

	// ── Handshake ────────────────────────────────────────────────────────
	_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
	req := &pubsubpb.SubscribeRequest{PeerId: s.config.NodeInfo.PeerID.String()}
	if err := pbstream.WriteDelimited(str, req); err != nil {
		str.Reset()
		cancel()
		return fmt.Errorf("pubsub subscribe: send request: %w", err)
	}
	_ = str.SetWriteDeadline(time.Time{})

	// Use a persistent bufio.Reader for all subsequent reads on this stream.
	// pbstream.ReadDelimited creates a new bufio.Reader on each call which
	// would silently discard bytes already buffered from the previous read.
	br := bufio.NewReader(str)

	_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp := &pubsubpb.SubscribeResponse{}
	if err := pbstream.ReadDelimited(br, resp); err != nil {
		str.Reset()
		cancel()
		return fmt.Errorf("pubsub subscribe: read response: %w", err)
	}
	_ = str.SetReadDeadline(time.Time{})

	if !resp.GetAccepted() {
		str.Close()
		cancel()
		return fmt.Errorf("pubsub subscribe: rejected by server: %s", resp.GetError())
	}

	s.config.isConnected = true
	Log.Logger(namedlogger).Info(ctx, "pubsub subscription accepted",
		ion.String("server", s.config.RemoteNode.ID().String()))

	// ── Receive loop ─────────────────────────────────────────────────────
	for {
		select {
		case <-ctx.Done():
			s.config.isConnected = false
			return ctx.Err()
		default:
		}

		env := &pubsubpb.BlockPubSubMessage{}
		if err := pbstream.ReadDelimited(br, env); err != nil {
			s.config.isConnected = false
			return fmt.Errorf("pubsub subscribe: stream read: %w", err)
		}

		switch p := env.GetPayload().(type) {
		case *pubsubpb.BlockPubSubMessage_Block:
			block := blocks.ProtoToZKBlock(p.Block)
			if err := s.config.WAL.Write(ctx, block); err != nil {
				Log.Logger(namedlogger).Warn(ctx, "pubsub: WAL write failed, block skipped",
					ion.Uint64("block_number", block.BlockNumber),
					ion.Err(err))
			}

		case *pubsubpb.BlockPubSubMessage_Done:
			Log.Logger(namedlogger).Info(ctx, "pubsub stream closed by server",
				ion.String("reason", p.Done.GetReason()))
			s.config.isConnected = false
			return nil
		}
	}
}

// EndSubscription cancels the subscription context and closes the stream.
func (s *Subscriber) EndSubscription() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.str != nil {
		s.str.Close()
		s.str = nil
	}
	s.config.isConnected = false
}

// ensure *Subscriber satisfies Subscriber_router at compile time.
var _ Subscriber_router = (*Subscriber)(nil)
