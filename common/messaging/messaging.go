package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
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
	// Create a context with 15-second timeout for the connection attempt
	// We do not retry here; if this fails, we return the error immediately with details.
	connectCtx, cancel := context.WithTimeout(ctx, constants.StreamDeadline)
	defer cancel()

	connectErr := host.Connect(connectCtx, targetPeer)

	// V2: If primary (QUIC) failed and we have TCP fallback, try it
	if connectErr != nil && version >= 2 && fallbackAddr != nil {
		fmt.Printf("Primary transport failed (err=%v), attempting TCP fallback...\n", connectErr)
		targetPeer.Addrs = []multiaddr.Multiaddr{fallbackAddr}
		// Reset timeout for fallback attempt
		fallbackCtx, fallbackCancel := context.WithTimeout(ctx, constants.StreamDeadline)
		defer fallbackCancel()
		connectErr = host.Connect(fallbackCtx, targetPeer)
		if connectErr != nil {
			return fmt.Errorf("failed to connect (QUIC and TCP fallback) to peer %s: %w", peerInfo.ID, connectErr)
		}
	} else if connectErr != nil {
		return fmt.Errorf("failed to connect to peer %s at %v: %w", peerInfo.ID, targetPeer.Addrs, connectErr)
	}

	// Create stream
	stream, err := host.NewStream(ctx, peerInfo.ID, protocolID)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}
	defer stream.Close()

	// Set write deadline
	if err := stream.SetWriteDeadline(time.Now().Add(constants.StreamDeadline)); err != nil {
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

// streamConfig holds the type-specific pieces needed by the shared send loop.
type streamConfig[E proto.Message] struct {
	newEnvelope   func() E
	isHeartbeat   func(E) bool
	mergeResponse func(E) error
}

// sendProtoDelimitedWithHeartbeatGeneric is the shared implementation for both
// SendProtoDelimitedWithHeartbeat and SendDataSyncProtoDelimitedWithHeartbeat.

// It sends a protocol buffer request and waits for a response, while keeping
// the connection alive by resetting the read deadline every time a heartbeat
// message is received. This is useful for long-running remote operations (e.g.
// heavy DB reads) where the remote peer sends periodic heartbeats to prevent
// timeouts.
//
// Parameters:
//   - ctx: Context for the stream configuration.
//   - version: Protocol version, used for transport selection (e.g. QUIC vs TCP).
//   - host: The libp2p host instance.
//   - peerInfo: The target peer's address info.
//   - protocolID: The protocol string identifier to negotiate.
//   - request: The protobuf message to send to the remote peer.
//   - cfg: A streamConfig containing type-specific callbacks for allocating,
//     evaluating, and merging the stream envelope type.
func sendProtoDelimitedWithHeartbeatGeneric[E proto.Message](
	ctx context.Context,
	version uint16,
	host host.Host,
	peerInfo peer.AddrInfo,
	protocolID protocol.ID,
	request proto.Message,
	cfg streamConfig[E],
) error {
	if host == nil {
		return errors.New("host is nil")
	}
	if request == nil {
		return errors.New("request message is nil")
	}
	if len(peerInfo.Addrs) == 0 {
		return errors.New("peer has no addresses")
	}

	// 1. Select primary and fallback transports based on protocol version
	primaryAddr, fallbackAddr, err := SelectTransportAddrWithFallback(peerInfo.Addrs, version)
	if err != nil {
		return fmt.Errorf("transport selection failed: %w", err)
	}

	targetPeer := peer.AddrInfo{
		ID:    peerInfo.ID,
		Addrs: []multiaddr.Multiaddr{primaryAddr},
	}

	// 2. Attempt connection with primary transport
	connectCtx, cancel := context.WithTimeout(ctx, constants.StreamDeadline)
	defer cancel()

	connectErr := host.Connect(connectCtx, targetPeer)

	// 3. Fallback to TCP if V2+ and primary (QUIC) failed
	if connectErr != nil && version >= 2 && fallbackAddr != nil {
		fmt.Printf("Primary transport failed (err=%v), attempting TCP fallback...\n", connectErr)
		targetPeer.Addrs = []multiaddr.Multiaddr{fallbackAddr}
		fallbackCtx, fallbackCancel := context.WithTimeout(ctx, constants.StreamDeadline)
		defer fallbackCancel()
		connectErr = host.Connect(fallbackCtx, targetPeer)
		if connectErr != nil {
			return fmt.Errorf("failed to connect (QUIC and TCP fallback) to peer %s: %w", peerInfo.ID, connectErr)
		}
	} else if connectErr != nil {
		return fmt.Errorf("failed to connect to peer %s at %v: %w", peerInfo.ID, targetPeer.Addrs, connectErr)
	}

	// 4. Create the stream
	stream, err := host.NewStream(ctx, peerInfo.ID, protocolID)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}
	defer stream.Close()

	// 5. Write the initial request
	if err := stream.SetWriteDeadline(time.Now().Add(constants.StreamDeadline)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	defer stream.SetWriteDeadline(time.Time{})

	if err := pbstream.WriteDelimited(stream, request); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	// 6. Heartbeat read loop
	// Read envelopes until we get the final response. Each heartbeat resets the read deadline.
	for {
		if err := stream.SetReadDeadline(time.Now().Add(constants.StreamDeadline)); err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}

		envelope := cfg.newEnvelope()
		if err := pbstream.ReadDelimited(stream, envelope); err != nil {
			return fmt.Errorf("failed to read stream message: %w", err)
		}

		if cfg.isHeartbeat(envelope) {
			// Heartbeat received — loop around to read next envelope and reset deadline.
			continue
		}

		// Final response — copy into caller's struct natively and exit.
		if err := cfg.mergeResponse(envelope); err != nil {
			return err
		}
		return nil
	}
}

// SendProtoDelimitedWithHeartbeat is a heartbeat-aware variant of SendProtoDelimited,
// designed for the PriorSync protocol.
func SendProtoDelimitedWithHeartbeat(
	ctx context.Context,
	version uint16,
	host host.Host,
	peerInfo peer.AddrInfo,
	protocolID protocol.ID,
	request proto.Message,
	response *priorsyncpb.PriorSyncMessage,
) error {
	if response == nil {
		return errors.New("response message is nil")
	}
	return sendProtoDelimitedWithHeartbeatGeneric(ctx, version, host, peerInfo, protocolID, request,
		streamConfig[*priorsyncpb.StreamMessage]{
			newEnvelope: func() *priorsyncpb.StreamMessage {
				return &priorsyncpb.StreamMessage{}
			},
			isHeartbeat: func(e *priorsyncpb.StreamMessage) bool {
				_, ok := e.Payload.(*priorsyncpb.StreamMessage_Heartbeat)
				return ok
			},
			mergeResponse: func(e *priorsyncpb.StreamMessage) error {
				p, ok := e.Payload.(*priorsyncpb.StreamMessage_Response)
				if !ok {
					return fmt.Errorf("unexpected StreamMessage payload type: %T", e.Payload)
				}
				if p.Response != nil {
					proto.Merge(response, p.Response)
				}
				return nil
			},
		},
	)
}

// SendDataSyncProtoDelimitedWithHeartbeat is a heartbeat-aware variant of SendProtoDelimited,
// designed for the DataSync protocol.
func SendDataSyncProtoDelimitedWithHeartbeat(
	ctx context.Context,
	version uint16,
	host host.Host,
	peerInfo peer.AddrInfo,
	protocolID protocol.ID,
	request proto.Message,
	response *datasyncpb.DataSyncResponse,
) error {
	if response == nil {
		return errors.New("response message is nil")
	}
	return sendProtoDelimitedWithHeartbeatGeneric(ctx, version, host, peerInfo, protocolID, request,
		streamConfig[*datasyncpb.DataSyncStreamMessage]{
			newEnvelope: func() *datasyncpb.DataSyncStreamMessage {
				return &datasyncpb.DataSyncStreamMessage{}
			},
			isHeartbeat: func(e *datasyncpb.DataSyncStreamMessage) bool {
				_, ok := e.Payload.(*datasyncpb.DataSyncStreamMessage_Heartbeat)
				return ok
			},
			mergeResponse: func(e *datasyncpb.DataSyncStreamMessage) error {
				p, ok := e.Payload.(*datasyncpb.DataSyncStreamMessage_Response)
				if !ok {
					return fmt.Errorf("unexpected StreamMessage payload type: %T", e.Payload)
				}
				if p.Response != nil {
					proto.Merge(response, p.Response)
				}
				return nil
			},
		},
	)
}
