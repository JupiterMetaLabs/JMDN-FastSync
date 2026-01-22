package protocol

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
)

// SendProto sends a protobuf message over a libp2p stream.
// If resp is non-nil, it will read a protobuf response into it.
func SendProto(
	ctx context.Context,
	h host.Host,
	to peer.AddrInfo,
	pid protocol.ID,
	req proto.Message,
	resp proto.Message,
) error {
	if ctx == nil {
		return fmt.Errorf("ctx is nil")
	}
	if h == nil {
		return fmt.Errorf("host is nil")
	}
	if req == nil {
		return fmt.Errorf("req is nil")
	}

	// Ensure we know peer addresses
	h.Peerstore().AddAddrs(to.ID, to.Addrs, time.Minute)

	// Connect (safe even if already connected)
	if err := h.Connect(ctx, to); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Open stream
	s, err := h.NewStream(ctx, to.ID, pid)
	if err != nil {
		return fmt.Errorf("new stream: %w", err)
	}
	defer s.Close()

	// Write request with deadline
	_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := pbstream.WriteDelimited(s, req); err != nil {
		return err
	}
	_ = s.SetWriteDeadline(time.Time{})

	// Optional read reply
	if resp != nil {
		_ = s.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := pbstream.ReadDelimited(s, resp); err != nil {
			return err
		}
		_ = s.SetReadDeadline(time.Time{})
	}

	return nil
}