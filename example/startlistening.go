package example

import (
	"context"
	"fmt"

	protocol "github.com/JupiterMetaLabs/JMDN-FastSync/core/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

const ProtocolID = "/priorsync/1.0.0"

type Node struct {
	host      host.Host
	priorSync protocol.Priorsync_router
	ctx       context.Context
	cancel    context.CancelFunc
}

// StartListening creates a new libp2p node and starts listening for PriorSync messages
func StartListening(ctx context.Context, port string) (*Node, error) {
	// Create libp2p host with QUIC transport
	listenAddr := fmt.Sprintf("/ip4/0.0.0.0/udp/%s/quic-v1", port)
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(listenAddr),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	// Create context for this node
	nodeCtx, cancel := context.WithCancel(ctx)

	// Create PriorSync router
	priorSyncRouter := protocol.NewPriorSyncRouter()

	// Set sync vars
	nodeInfo := types.Nodeinfo{
		PeerID:       h.ID(),
		Multiaddr:    h.Addrs(),
		Capabilities: []string{"priorsync"},
		Version:      1,
		Protocol:     libp2pprotocol.ID(ProtocolID),
	}

	priorSyncRouter.SetSyncVars(
		nodeCtx,
		libp2pprotocol.ID(ProtocolID),
		1,
		nodeInfo,
	)

	// Start handling incoming PriorSync messages
	go func() {
		if err := priorSyncRouter.HandlePriorSync(h); err != nil {
			fmt.Printf("HandlePriorSync error: %v\n", err)
		}
	}()

	node := &Node{
		host:      h,
		priorSync: priorSyncRouter,
		ctx:       nodeCtx,
		cancel:    cancel,
	}

	fmt.Println("Node started successfully!")
	fmt.Printf("Listening on:\n")
	for _, addr := range h.Addrs() {
		fmt.Printf("  %s/p2p/%s\n", addr, h.ID())
	}

	return node, nil
}

// Stop gracefully shuts down the node
func (n *Node) Stop() {
	fmt.Println("Stopping node...")
	n.priorSync.Close()
	n.cancel()
	if err := n.host.Close(); err != nil {
		fmt.Printf("Error closing host: %v\n", err)
	}
	fmt.Println("Node stopped.")
}

// GetAddress returns the multiaddr of this node
func (n *Node) GetAddress() string {
	if len(n.host.Addrs()) == 0 {
		return "no addresses"
	}
	return fmt.Sprintf("%s/p2p/%s", n.host.Addrs()[0], n.host.ID())
}

// GetPeerID returns the peer ID of this node
func (n *Node) GetPeerID() string {
	return n.host.ID().String()
}

// GetHost returns the underlying libp2p host
func (n *Node) GetHost() host.Host {
	return n.host
}

// GetPriorSync returns the PriorSync router
func (n *Node) GetPriorSync() protocol.Priorsync_router {
	return n.priorSync
}
