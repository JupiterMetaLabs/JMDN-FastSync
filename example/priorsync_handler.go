package example

import (
	"fmt"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p/core/network"
)

// PriorSyncHandler implements the messaging.ProtocolHandler interface for PriorSync protocol.
type PriorSyncHandler struct {
	dataRouter *router.Datarouter
}

// NewPriorSyncHandler creates a new PriorSync protocol handler.
//
// Parameters:
//   - nodeInfo: Node information for routing
//
// Returns:
//   - Initialized PriorSyncHandler
func NewPriorSyncHandler(nodeInfo *types.Nodeinfo) *PriorSyncHandler {
	return &PriorSyncHandler{
		dataRouter: router.NewDatarouter(nodeInfo),
	}
}

// HandleStream processes an incoming PriorSync stream.
// This implements the messaging.ProtocolHandler interface.
func (h *PriorSyncHandler) HandleStream(s network.Stream) {
	defer s.Close()

	// Set read deadline
	_ = s.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer s.SetReadDeadline(time.Time{})

	// Read request
	req := &priorsyncpb.PriorSync{}
	if err := pbstream.ReadDelimited(s, req); err != nil {
		fmt.Printf("Failed to read PriorSync request: %v\n", err)
		return
	}

	// Route to data router for state-based processing
	resp := h.dataRouter.HandlePriorSync(req)

	// Set write deadline
	_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer s.SetWriteDeadline(time.Time{})

	// Send response
	if err := pbstream.WriteDelimited(s, resp); err != nil {
		fmt.Printf("Failed to write PriorSync response: %v\n", err)
		return
	}
}
