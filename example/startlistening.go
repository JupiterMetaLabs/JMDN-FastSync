package example

import (
	"context"
	"fmt"

	priorsync "github.com/JupiterMetaLabs/JMDN-FastSync/core/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// StartListening creates a new generic messaging node and registers the PriorSync protocol handler.
// This demonstrates how to use the generic messaging package for a specific protocol.
//
// Parameters:
//   - ctx: Parent context for the node lifecycle
//   - port: UDP port to listen on (e.g., "4001")
//
// Returns:
//   - Initialized messaging.Node with PriorSync protocol registered
//   - Error if node creation or protocol registration fails
func StartListening(ctx context.Context, port string, version uint16) (*Node, error) {
	// Create generic messaging node
	node, err := NewNode(ctx, port)
	if err != nil {
		return nil, fmt.Errorf("failed to create node: %w", err)
	}

	// Create node info for this node
	nodeInfo := types.Nodeinfo{
		PeerID:       node.GetHost().ID(),
		Multiaddr:    node.GetHost().Addrs(),
		Capabilities: []string{"priorsync"},
		Version:      version,
		Protocol:     protocol.ID(ProtocolID),
		BlockInfo:    NewExampleBlockInfo(),
	}

	// Create PriorSync protocol handler from core
	ps := priorsync.NewPriorSyncRouter()
	ps.SetSyncVars(node.GetContext(), protocol.ID(ProtocolID), version, nodeInfo)

	// Start handling in background - core handler manages its own stream handler registration
	go func() {
		if err := ps.HandlePriorSync(node.GetHost()); err != nil {
			// Context cancellation is normal during shutdown
			if ctx.Err() == nil {
				fmt.Printf("PriorSync handler error: %v\n", err)
			}
		}
	}()

	fmt.Printf("PriorSync protocol registered and listening on %s\n", ProtocolID)

	return node, nil
}

// StartListeningWithCustomHandler creates a node and registers a custom protocol handler.
// This demonstrates the flexibility of the generic messaging package.
//
// Parameters:
//   - ctx: Parent context for the node lifecycle
//   - port: UDP port to listen on
//   - protoID: Protocol ID to register
//   - handler: Custom protocol handler
//
// Returns:
//   - Initialized messaging.Node with custom protocol registered
//   - Error if node creation fails
func StartListeningWithCustomHandler(
	ctx context.Context,
	port string,
	protoID protocol.ID,
	handler ProtocolHandler,
) (*Node, error) {
	// Create example node
	node, err := NewNode(ctx, port)
	if err != nil {
		return nil, fmt.Errorf("failed to create node: %w", err)
	}

	// Register custom protocol
	node.RegisterProtocol(protoID, handler)

	fmt.Printf("Custom protocol %s registered and listening\n", protoID)

	return node, nil
}
