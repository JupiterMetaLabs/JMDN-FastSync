package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"
)

// SendMessage sends raw bytes to a peer using the specified protocol.
// It handles connection establishment, stream creation, message exchange, and cleanup.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - host: The libp2p host to send from
//   - peerAddr: Multiaddr string of the peer (e.g., "/ip4/127.0.0.1/udp/4001/quic-v1/p2p/QmPeerID")
//   - protocolID: The protocol ID to use for communication
//   - payload: Raw bytes to send
//
// Returns:
//   - Response bytes from the peer
//   - Error if any step fails
func SendMessage(
	ctx context.Context,
	host host.Host,
	peerAddr string,
	protocolID protocol.ID,
	payload []byte,
) ([]byte, error) {
	if host == nil {
		return nil, errors.New("host is nil")
	}
	if peerAddr == "" {
		return nil, errors.New("peer address is empty")
	}
	if protocolID == "" {
		return nil, errors.New("protocol ID is empty")
	}

	// Parse multiaddr
	maddr, err := multiaddr.NewMultiaddr(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Extract peer info
	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return nil, fmt.Errorf("failed to extract peer info: %w", err)
	}

	// Connect to peer
	if err := host.Connect(ctx, *peerInfo); err != nil {
		return nil, fmt.Errorf("failed to connect to peer: %w", err)
	}

	// Send to the connected peer
	return SendMessageToPeer(ctx, host, peerInfo.ID, protocolID, payload)
}

// SendMessageToPeer sends raw bytes to an already-connected peer.
// Use this when you've already established a connection to avoid redundant connection attempts.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - host: The libp2p host to send from
//   - peerID: The peer ID to send to
//   - protocolID: The protocol ID to use for communication
//   - payload: Raw bytes to send
//
// Returns:
//   - Response bytes from the peer
//   - Error if any step fails
func SendMessageToPeer(
	ctx context.Context,
	host host.Host,
	peerID peer.ID,
	protocolID protocol.ID,
	payload []byte,
) ([]byte, error) {
	if host == nil {
		return nil, errors.New("host is nil")
	}
	if peerID == "" {
		return nil, errors.New("peer ID is empty")
	}
	if protocolID == "" {
		return nil, errors.New("protocol ID is empty")
	}

	// Create stream
	stream, err := host.NewStream(ctx, peerID, protocolID)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}
	defer stream.Close()

	// Set write deadline
	if err := stream.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, fmt.Errorf("failed to set write deadline: %w", err)
	}
	defer stream.SetWriteDeadline(time.Time{})

	// Write payload
	if _, err := stream.Write(payload); err != nil {
		return nil, fmt.Errorf("failed to write payload: %w", err)
	}

	// Close write side to signal we're done sending
	if err := stream.CloseWrite(); err != nil {
		return nil, fmt.Errorf("failed to close write: %w", err)
	}

	// Set read deadline
	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, fmt.Errorf("failed to set read deadline: %w", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	// Read response
	response := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			response = append(response, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
	}

	return response, nil
}

// SendProtoDelimited sends a length-delimited protobuf message and receives a length-delimited response.
// This uses the pbstream package for proper protobuf stream handling.
//
// Transport selection based on version:
//   - V1 (version == 1): Uses TCP transport only
//   - V2 (version >= 2): Prioritizes QUIC transport, automatically falls back to TCP if QUIC fails
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - version: Protocol version (1 = TCP only, 2+ = QUIC with TCP fallback)
//   - host: The libp2p host to send from
//   - peerInfo: Peer information including ID and multiple multiaddrs
//   - protocolID: The protocol ID to use for communication
//   - request: Protobuf message to send
//   - response: Protobuf message to unmarshal response into
//
// Returns:
//   - Error if any step fails
func SendProtoDelimited(
	ctx context.Context,
	version uint16,
	host host.Host,
	peerInfo peer.AddrInfo,
	protocolID protocol.ID,
	request proto.Message,
	response proto.Message,
) error {
	if host == nil {
		return errors.New("host is nil")
	}
	if request == nil {
		return errors.New("request message is nil")
	}
	if response == nil {
		return errors.New("response message is nil")
	}
	if len(peerInfo.Addrs) == 0 {
		return errors.New("peer has no addresses")
	}

	// Select transport addresses based on version
	primaryAddr, fallbackAddr, err := SelectTransportAddrWithFallback(peerInfo.Addrs, version)
	if err != nil {
		return fmt.Errorf("transport selection failed: %w", err)
	}

	// Create peer info with primary address
	targetPeer := peer.AddrInfo{
		ID:    peerInfo.ID,
		Addrs: []multiaddr.Multiaddr{primaryAddr},
	}

	// Attempt connection with primary transport
	connectErr := host.Connect(ctx, targetPeer)

	// V2: If primary (QUIC) failed and we have TCP fallback, try it
	if connectErr != nil && version >= 2 && fallbackAddr != nil {
		fmt.Printf("Primary transport failed, attempting TCP fallback...\n")
		targetPeer.Addrs = []multiaddr.Multiaddr{fallbackAddr}
		connectErr = host.Connect(ctx, targetPeer)
		if connectErr != nil {
			return fmt.Errorf("failed to connect (QUIC and TCP fallback): %w", connectErr)
		}
	} else if connectErr != nil {
		return fmt.Errorf("failed to connect: %w", connectErr)
	}

	// Create stream
	stream, err := host.NewStream(ctx, peerInfo.ID, protocolID)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}
	defer stream.Close()

	// Set write deadline
	if err := stream.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	defer stream.SetWriteDeadline(time.Time{})

	// Write delimited request
	if err := pbstream.WriteDelimited(stream, request); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	// Set read deadline
	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("failed to set read deadline: %w", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	// Read delimited response
	if err := pbstream.ReadDelimited(stream, response); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	return nil
}