package example

import (
	"context"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// SendMessage sends a PriorSync message to a peer
func SendMessage(ctx context.Context, node *Node, peerAddr string, state string) error {
	// Parse the multiaddr
	maddr, err := multiaddr.NewMultiaddr(peerAddr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Extract peer info from multiaddr
	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("failed to get peer info: %w", err)
	}

	// Connect to the peer
	if err := node.GetHost().Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	fmt.Printf("Connected to peer: %s\n", peerInfo.ID)

	// Create PriorSync data matching the structure in data_router.go
	data := types.PriorSync{
		Blocknumber: 100,
		Stateroot:   []byte("example-state-root"),
		Blockhash:   []byte("example-block-hash"),
		Metadata: types.Metadata{
			Checksum: []byte("example-checksum"),
			State:    constants.SYNC_REQUEST,
			Version:  1,
		},
	}

	// Create Nodeinfo for the peer
	peerNodeInfo := types.Nodeinfo{
		PeerID:    peerInfo.ID,
		Multiaddr: peerInfo.Addrs,
	}

	// Send the message
	fmt.Printf("Sending PriorSync message with state: %s\n", constants.SYNC_REQUEST)
	resp, err := node.GetPriorSync().SendPriorSync(peerNodeInfo, data)
	if err != nil {
		return fmt.Errorf("failed to send priorsync: %w", err)
	}

	// Display the response from the server
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
