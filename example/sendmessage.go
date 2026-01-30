package example

import (
	"context"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"
)

const ProtocolID = "/priorsync/1.0.0"

// SendPriorSyncMessage sends a PriorSync message to a peer using the generic messaging package.
// This is a PriorSync-specific wrapper around the generic messaging functionality.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - node: The messaging node to send from
//   - peerAddr: Multiaddr string of the peer
//   - data: PriorSync data to send
//
// Returns:
//   - PriorSyncMessage response from the peer
//   - Error if sending fails
func SendPriorSyncMessage(
	ctx context.Context,
	node *Node,
	peerAddrs []multiaddr.Multiaddr,
	data types.PriorSync,
) (*types.PriorSyncMessage, error) {
	// Convert types.PriorSync to protobuf
	req := &priorsyncpb.PriorSync{
		Blocknumber: data.Blocknumber,
		Stateroot:   data.Stateroot,
		Blockhash:   data.Blockhash,
		Metadata: &priorsyncpb.Metadata{
			Checksum: data.Metadata.Checksum,
			State:    data.Metadata.State,
			Version:  uint32(data.Metadata.Version),
		},
	}

	// Parse peer address to get AddrInfo
	peerInfo, err := messaging.ParseMultiaddrs(peerAddrs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse peer address: %w", err)
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using generic protobuf delimited messaging with version
	// Version defaults to 1 if not set in data.Metadata
	version := data.Metadata.Version
	if version == 0 {
		version = 1
	}

	if version > 2 || version < 1 {
		return nil, fmt.Errorf("invalid version: %d", version)
	}

	if err := messaging.SendProtoDelimited(
		ctx,
		version,
		node.GetHost(),
		peerInfo,
		protocol.ID(ProtocolID),
		req,
		resp,
	); err != nil {
		return nil, fmt.Errorf("failed to send priorsync: %w", err)
	}

	// Check acknowledgment
	if resp.Ack == nil {
		return nil, fmt.Errorf("no acknowledgment received")
	}
	if !resp.Ack.Ok {
		return nil, fmt.Errorf("sync failed: %s", resp.Ack.Error)
	}

	// Convert protobuf response to types.PriorSyncMessage
	result := &types.PriorSyncMessage{
		Ack: &types.PriorSyncAck{
			State: resp.Ack.State,
			Ok:    resp.Ack.Ok,
			Error: resp.Ack.Error,
		},
	}

	// Convert PriorSync if present
	if resp.Priorsync != nil {
		result.Priorsync = &types.PriorSync{
			Blocknumber: resp.Priorsync.Blocknumber,
			Stateroot:   resp.Priorsync.Stateroot,
			Blockhash:   resp.Priorsync.Blockhash,
			Metadata: types.Metadata{
				Checksum: resp.Priorsync.Metadata.Checksum,
				State:    resp.Priorsync.Metadata.State,
				Version:  uint16(resp.Priorsync.Metadata.Version),
			},
		}
	}

	return result, nil
}

// SendMessage is a convenience function that creates example PriorSync data and sends it.
// This maintains backward compatibility with the original SendMessage function.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - node: The messaging node to send from
//   - peerAddrs: List of Multiaddr strings of the peer
//   - state: The state to send (e.g., constants.SYNC_REQUEST)
//
// Returns:
//   - Error if sending fails
func SendMessage(ctx context.Context, node *Node, state string) error {
	// Prepare data for checksum calculation (protobuf format without metadata)
	protoData := &priorsyncpb.PriorSync{
		Blocknumber: 100,
		Stateroot:   []byte("example-state-root"),
		Blockhash:   []byte("example-block-hash"),
		Metadata:    nil, // Metadata is excluded from checksum
	}

	// Marshal to bytes
	dataBytes, err := proto.Marshal(protoData)
	if err != nil {
		return fmt.Errorf("failed to marshal data for checksum: %w", err)
	}

	// Calculate checksum using SHA256 (Version 2)
	cs, err := checksum.NewChecksum().Create(dataBytes, checksum.VersionSHA256)
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Create example PriorSync data with valid checksum
	data := types.PriorSync{
		Blocknumber: protoData.Blocknumber,
		Stateroot:   protoData.Stateroot,
		Blockhash:   protoData.Blockhash,
		Metadata: types.Metadata{
			Checksum: cs,
			State:    state,
			Version:  2,
		},
	}

	fmt.Printf("Sending PriorSync message with state: %s\n", state)
	peerAddrs := node.host.Addrs()
	// Send the message
	resp, err := SendPriorSyncMessage(ctx, node, peerAddrs, data)
	if err != nil {
		return fmt.Errorf("failed to send priorsync: %w", err)
	}

	// Display the response
	fmt.Println("\n✓ Message sent successfully!")
	fmt.Println("=== Server Response ===")
	if resp.Ack != nil {
		fmt.Printf("ACK State: %s\n", resp.Ack.State)
		fmt.Printf("ACK OK: %v\n", resp.Ack.Ok)
		if resp.Ack.Error != "" {
			fmt.Printf("ACK Error: %s\n", resp.Ack.Error)
		}
	}

	if resp.Priorsync != nil {
		fmt.Println("\n=== Response Data ===")
		fmt.Printf("Block Number: %d\n", resp.Priorsync.Blocknumber)
		fmt.Printf("State Root: %s\n", string(resp.Priorsync.Stateroot))
		fmt.Printf("Block Hash: %s\n", string(resp.Priorsync.Blockhash))
		fmt.Printf("Response State: %s\n", resp.Priorsync.Metadata.State)
	}

	return nil
}

// SendRawBytes sends raw bytes using the PriorSync protocol.
// This demonstrates how to use the generic messaging package directly.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - node: The messaging node to send from
//   - peerAddr: Multiaddr string of the peer
//   - payload: Raw bytes to send
//
// Returns:
//   - Response bytes from the peer
//   - Error if sending fails
func SendRawBytes(
	ctx context.Context,
	node *Node,
	peerAddr string,
	payload []byte,
) ([]byte, error) {
	return messaging.SendMessage(
		ctx,
		node.GetHost(),
		peerAddr,
		protocol.ID(ProtocolID),
		payload,
	)
}
