package messaging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	"github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
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
		logging.Logger(logging.Transport).Warn(ctx, "primary transport failed, attempting TCP fallback",
			ion.Err(connectErr))
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
	if err := stream.SetReadDeadline(time.Now().Add(constants.StreamDeadline)); err != nil {
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
	// isFinal reports whether this envelope is the last frame on the stream.
	// nil means every non-heartbeat frame is final (correct for all single-response
	// protocols: PriorSync, DataSync, PoTS). Set explicitly for multi-frame protocols
	// like AccountSync where the loop must continue past batch_ack / response frames
	// and only exit on the end-of-stream frame.
	isFinal func(E) bool
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
		logging.Logger(logging.Transport).Warn(ctx, "primary transport failed, attempting TCP fallback",
			ion.Err(connectErr))
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

		if err := cfg.mergeResponse(envelope); err != nil {
			return err
		}
		// nil isFinal → every non-heartbeat frame is final (PriorSync, DataSync, PoTS).
		// Non-nil isFinal → only exit when the frame is the designated terminator (AccountSync end).
		if cfg.isFinal == nil || cfg.isFinal(envelope) {
			return nil
		}
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

// SendPoTSProtoDelimitedWithHeartbeat is a heartbeat-aware variant of SendProtoDelimited,
// designed for the PoTS (Proof of Time Sync) protocol.
func SendPoTSProtoDelimitedWithHeartbeat(
	ctx context.Context,
	version uint16,
	host host.Host,
	peerInfo peer.AddrInfo,
	protocolID protocol.ID,
	request proto.Message,
	response *potspb.PoTSResponse,
) error {
	if response == nil {
		return errors.New("response message is nil")
	}
	return sendProtoDelimitedWithHeartbeatGeneric(ctx, version, host, peerInfo, protocolID, request,
		streamConfig[*potspb.PoTSStreamMessage]{
			newEnvelope: func() *potspb.PoTSStreamMessage {
				return &potspb.PoTSStreamMessage{}
			},
			isHeartbeat: func(e *potspb.PoTSStreamMessage) bool {
				_, ok := e.Payload.(*potspb.PoTSStreamMessage_Heartbeat)
				return ok
			},
			mergeResponse: func(e *potspb.PoTSStreamMessage) error {
				p, ok := e.Payload.(*potspb.PoTSStreamMessage_Response)
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

// accountSyncStream is the minimal interface required by
// ReadAccountsSyncServerMessageSkippingHeartbeats: a readable stream whose
// read deadline can be reset on each heartbeat to keep the connection alive.
// libp2p network.Stream satisfies this interface.
type accountSyncStream interface {
	io.Reader
	SetReadDeadline(t time.Time) error
}

// ReadAccountsSyncServerMessageSkippingHeartbeats reads the next non-heartbeat
// AccountSyncServerMessage from an already-open AccountSync stream.
//
// Heartbeat frames are silently consumed; the read deadline is reset on each one
// to prevent the stream from timing out during long diff computations on the server.
//
// All other payload types are returned as-is — inspect with:
//
//	msg.GetBatchAck()  → AccountBatchAck       (server acked one ART batch)
//	msg.GetResponse()  → AccountSyncResponse   (one page of missing accounts)
//	msg.GetEnd()       → AccountSyncEndOfStream (completion signal — stop looping)
//
// Call this in a loop on the same stream after sending the final
// AccountNonceSyncRequest (is_last=true) until msg.GetEnd() is non-nil or error.
func ReadAccountsSyncServerMessageSkippingHeartbeats(stream accountSyncStream) (*accountspb.AccountSyncServerMessage, error) {
	for {
		if err := stream.SetReadDeadline(time.Now().Add(constants.StreamDeadline)); err != nil {
			return nil, fmt.Errorf("accounts sync: set read deadline: %w", err)
		}
		msg := &accountspb.AccountSyncServerMessage{}
		if err := pbstream.ReadDelimited(stream, msg); err != nil {
			return nil, fmt.Errorf("accounts sync: read frame: %w", err)
		}
		if _, ok := msg.Payload.(*accountspb.AccountSyncServerMessage_Heartbeat); ok {
			// Heartbeat — deadline already reset above; read the next frame.
			continue
		}
		return msg, nil
	}
}

// SendAccountsSyncAllChunksWithHeartbeat uploads all ART chunks to the server on a
// single persistent stream and dispatches every server→client frame via handlers.
//
// Protocol:
//
//	For each non-final chunk (is_last=false):
//	  write chunk → read BatchAck (heartbeats skipped) → handlers.OnBatchAck → next chunk
//
//	For the final chunk (is_last=true):
//	  write chunk → read frames until EndOfStream:
//	    batch_ack  → handlers.OnBatchAck, loop
//	    response   → handlers.OnResponse, loop
//	    end        → handlers.OnEnd, return nil
//
// All chunks share ONE stream so the server's sessionLockedART accumulates every
// batch before the diff computation begins.
func SendAccountsSyncAllChunksWithHeartbeat(
	ctx context.Context,
	version uint16,
	host host.Host,
	peerInfo peer.AddrInfo,
	protocolID protocol.ID,
	chunks <-chan *accountspb.AccountNonceSyncRequest,
	handlers AccountSyncHandlers,
) error {
	if host == nil {
		return errors.New("host is nil")
	}
	if len(peerInfo.Addrs) == 0 {
		return errors.New("peer has no addresses")
	}

	primaryAddr, fallbackAddr, err := SelectTransportAddrWithFallback(peerInfo.Addrs, version)
	if err != nil {
		return fmt.Errorf("transport selection failed: %w", err)
	}

	targetPeer := peer.AddrInfo{ID: peerInfo.ID, Addrs: []multiaddr.Multiaddr{primaryAddr}}

	connectCtx, cancel := context.WithTimeout(ctx, constants.StreamDeadline)
	defer cancel()

	connectErr := host.Connect(connectCtx, targetPeer)
	if connectErr != nil && version >= 2 && fallbackAddr != nil {
		logging.Logger(logging.Transport).Warn(ctx, "primary transport failed, attempting TCP fallback", ion.Err(connectErr))
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

	stream, err := host.NewStream(ctx, peerInfo.ID, protocolID)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}
	defer stream.Close()

	for chunk := range chunks {
		if err := stream.SetWriteDeadline(time.Now().Add(constants.StreamDeadline)); err != nil {
			return fmt.Errorf("accountsync: set write deadline: %w", err)
		}
		if err := pbstream.WriteDelimited(stream, chunk); err != nil {
			return fmt.Errorf("accountsync: write chunk %d: %w", chunk.BatchIndex, err)
		}

		if !chunk.IsLast {
			msg, err := ReadAccountsSyncServerMessageSkippingHeartbeats(stream)
			if err != nil {
				return fmt.Errorf("accountsync: read ack for chunk %d: %w", chunk.BatchIndex, err)
			}
			batchAck := msg.GetBatchAck()
			if batchAck == nil {
				return fmt.Errorf("accountsync: expected BatchAck for chunk %d, got %T", chunk.BatchIndex, msg.Payload)
			}
			if handlers.OnBatchAck != nil {
				if err := handlers.OnBatchAck(batchAck); err != nil {
					return err
				}
			}
		} else {
			for {
				msg, err := ReadAccountsSyncServerMessageSkippingHeartbeats(stream)
				if err != nil {
					return fmt.Errorf("accountsync: read final response: %w", err)
				}
				switch p := msg.Payload.(type) {
				case *accountspb.AccountSyncServerMessage_BatchAck:
					if handlers.OnBatchAck != nil {
						if err := handlers.OnBatchAck(p.BatchAck); err != nil {
							return err
						}
					}
				case *accountspb.AccountSyncServerMessage_Response:
					if handlers.OnResponse != nil {
						if err := handlers.OnResponse(p.Response); err != nil {
							return err
						}
					}
				case *accountspb.AccountSyncServerMessage_End:
					if handlers.OnEnd != nil {
						if err := handlers.OnEnd(p.End); err != nil {
							return err
						}
					}
					return nil
				default:
					return fmt.Errorf("accountsync: unexpected frame type %T after final chunk", msg.Payload)
				}
			}
		}
	}
	return fmt.Errorf("accountsync: chunks channel closed without a final chunk")
}

// AccountSyncHandlers holds per-frame callbacks for SendAccountsSyncAllChunksWithHeartbeat.
// Heartbeat frames are silently consumed; no callback is needed for them.
// Returning a non-nil error from any handler aborts the read loop immediately.
type AccountSyncHandlers struct {
	// OnBatchAck is called for each AccountBatchAck (one per uploaded ART batch).
	OnBatchAck func(*accountspb.AccountBatchAck) error
	// OnResponse is called for each AccountSyncResponse page of missing accounts.
	// Pages may arrive out of order; use page_index to reorder if needed.
	OnResponse func(*accountspb.AccountSyncResponse) error
	// OnEnd is called exactly once when AccountSyncEndOfStream arrives.
	// Use total_pages to verify all response pages were received before the
	// function returns.
	OnEnd func(*accountspb.AccountSyncEndOfStream) error
}

// streamReadWriter is the minimal interface WriteAccountPageAndReadACK needs.
// libp2p network.Stream and any types.AccountSyncStream implementation satisfy it.
type streamReadWriter interface {
	io.ReadWriter
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// OpenAccountsSyncDataStream connects to peerInfo and opens a stream on
// AccountsSyncDataProtocol without closing it — the caller owns the lifetime.
//
// Use this to open a persistent per-worker stream. Call WriteAccountPageAndReadACK
// in a loop on the returned stream, then Close() when the worker is done.
func OpenAccountsSyncDataStream(
	ctx context.Context,
	version uint16,
	h host.Host,
	peerInfo peer.AddrInfo,
) (network.Stream, error) {
	if h == nil {
		return nil, errors.New("host is nil")
	}
	if len(peerInfo.Addrs) == 0 {
		return nil, errors.New("peer has no addresses")
	}

	primaryAddr, fallbackAddr, err := SelectTransportAddrWithFallback(peerInfo.Addrs, version)
	if err != nil {
		return nil, fmt.Errorf("transport selection failed: %w", err)
	}

	targetPeer := peer.AddrInfo{ID: peerInfo.ID, Addrs: []multiaddr.Multiaddr{primaryAddr}}

	connectCtx, cancel := context.WithTimeout(ctx, constants.StreamDeadline)
	defer cancel()

	connectErr := h.Connect(connectCtx, targetPeer)
	if connectErr != nil && version >= 2 && fallbackAddr != nil {
		logging.Logger(logging.Transport).Warn(ctx, "primary transport failed, attempting TCP fallback",
			ion.Err(connectErr))
		targetPeer.Addrs = []multiaddr.Multiaddr{fallbackAddr}
		fallbackCtx, fallbackCancel := context.WithTimeout(ctx, constants.StreamDeadline)
		defer fallbackCancel()
		connectErr = h.Connect(fallbackCtx, targetPeer)
		if connectErr != nil {
			return nil, fmt.Errorf("failed to connect (QUIC and TCP fallback) to peer %s: %w", peerInfo.ID, connectErr)
		}
	} else if connectErr != nil {
		return nil, fmt.Errorf("failed to connect to peer %s: %w", peerInfo.ID, connectErr)
	}

	stream, err := h.NewStream(ctx, peerInfo.ID, constants.AccountsSyncDataProtocol)
	if err != nil {
		return nil, fmt.Errorf("open AccountsSyncDataProtocol stream: %w", err)
	}
	return stream, nil
}

// WriteAccountPageAndReadACK writes one AccountSyncServerMessage to an open
// stream and reads back the client's AccountSyncServerMessage ACK.
//
// Does NOT open or close the stream — the caller owns its lifetime.
// deadline is applied independently to the write and read operations.
func WriteAccountPageAndReadACK(
	stream streamReadWriter,
	msg *accountspb.AccountSyncServerMessage,
	deadline time.Duration,
) (*accountspb.AccountSyncServerMessage, error) {
	if err := stream.SetWriteDeadline(time.Now().Add(deadline)); err != nil {
		return nil, fmt.Errorf("set write deadline: %w", err)
	}
	if err := pbstream.WriteDelimited(stream, msg); err != nil {
		return nil, fmt.Errorf("write account page: %w", err)
	}

	if err := stream.SetReadDeadline(time.Now().Add(deadline)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	ack := &accountspb.AccountSyncServerMessage{}
	if err := pbstream.ReadDelimited(stream, ack); err != nil {
		return nil, fmt.Errorf("read account page ACK: %w", err)
	}
	return ack, nil
}
