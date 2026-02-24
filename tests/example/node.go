package example

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolHandler defines the interface for handling incoming streams for a protocol.
// This is used by the example Node to demonstrate multi-protocol support.
type ProtocolHandler interface {
	// HandleStream processes an incoming stream for this protocol.
	HandleStream(stream network.Stream)
}

// Node represents a simple libp2p node for examples and testing.
// For production use, integrate directly with your blockchain node's host.
type Node struct {
	host      host.Host
	ctx       context.Context
	cancel    context.CancelFunc
	protocols map[protocol.ID]ProtocolHandler
	mu        sync.RWMutex
}

// NewNode creates a new standalone libp2p node for examples and testing.
// For production blockchain integration, use your blockchain's existing host directly
// with the messaging.SendProtoDelimited function.
func NewNode(ctx context.Context, port string) (*Node, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if port == "" {
		return nil, errors.New("port is empty")
	}

	// Create libp2p host with QUIC transport AND TCP transport for fallback
	listenAddrQUIC := fmt.Sprintf("/ip4/0.0.0.0/udp/%s/quic-v1", port)
	listenAddrTCP := fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", port)
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(listenAddrQUIC, listenAddrTCP),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	// Create node context
	nodeCtx, cancel := context.WithCancel(ctx)

	node := &Node{
		host:      h,
		ctx:       nodeCtx,
		cancel:    cancel,
		protocols: make(map[protocol.ID]ProtocolHandler),
		mu:        sync.RWMutex{},
	}

	fmt.Println("Example node started successfully!")
	fmt.Printf("Listening on:\n")
	var allAddrs []string
	for _, addr := range h.Addrs() {
		fullAddr := fmt.Sprintf("%s/p2p/%s", addr, h.ID())
		fmt.Printf("  %s\n", fullAddr)
		allAddrs = append(allAddrs, fullAddr)
	}
	fmt.Printf("\nFor CLI usage (copy this for multi-transport fallback):\n%s\n", strings.Join(allAddrs, ","))

	return node, nil
}

// RegisterProtocol registers a protocol handler for the specified protocol ID.
func (n *Node) RegisterProtocol(protoID protocol.ID, handler ProtocolHandler) {
	if protoID == "" {
		fmt.Println("Warning: attempted to register empty protocol ID")
		return
	}
	if handler == nil {
		fmt.Println("Warning: attempted to register nil handler")
		return
	}

	n.mu.Lock()
	n.protocols[protoID] = handler
	n.mu.Unlock()

	// Set stream handler
	n.host.SetStreamHandler(protoID, func(s network.Stream) {
		select {
		case <-n.ctx.Done():
			s.Close()
			return
		default:
		}
		handler.HandleStream(s)
	})

	fmt.Printf("Registered protocol: %s\n", protoID)
}

// GetHost returns the underlying libp2p host.
func (n *Node) GetHost() host.Host {
	return n.host
}

// GetContext returns the node's context.
func (n *Node) GetContext() context.Context {
	return n.ctx
}

// GetAddress returns the first multiaddr of this node with peer ID.
func (n *Node) GetAddress() string {
	if len(n.host.Addrs()) == 0 {
		return ""
	}
	return fmt.Sprintf("%s/p2p/%s", n.host.Addrs()[0], n.host.ID())
}

// GetPeerID returns the peer ID of this node as a string.
func (n *Node) GetPeerID() string {
	return n.host.ID().String()
}

// Stop gracefully shuts down the node.
func (n *Node) Stop() {
	fmt.Println("Stopping example node...")
	n.cancel()

	n.mu.Lock()
	for protoID := range n.protocols {
		n.host.RemoveStreamHandler(protoID)
	}
	n.protocols = make(map[protocol.ID]ProtocolHandler)
	n.mu.Unlock()

	if err := n.host.Close(); err != nil {
		fmt.Printf("Error closing host: %v\n", err)
	}
	fmt.Println("Example node stopped.")
}
