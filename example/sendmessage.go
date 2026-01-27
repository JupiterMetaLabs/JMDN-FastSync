package example

import (
	"context"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
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

	// Create PriorSync data
	data := types.PriorSync{
		Blocknumber: 12345,
		Stateroot:   []byte("example-state-root"),
		Blockhash:   []byte("example-block-hash"),
		Metadata: types.Metadata{
			Checksum: []byte("example-checksum"),
			State:    state,
			Version:  1,
		},
	}

	// Create Nodeinfo for the peer
	peerNodeInfo := types.Nodeinfo{
		PeerID:    peerInfo.ID,
		Multiaddr: peerInfo.Addrs,
	}

	// Send the message
	fmt.Printf("Sending PriorSync message with state: %s\n", state)
	if err := node.GetPriorSync().SendPriorSync(peerNodeInfo, data); err != nil {
		return fmt.Errorf("failed to send priorsync: %w", err)
	}

	fmt.Println("✓ Message sent and acknowledged!")
	return nil
}
