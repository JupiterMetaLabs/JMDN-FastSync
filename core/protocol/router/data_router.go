package router

import (
	"fmt"

	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
)

type Datarouter struct{}

func NewDatarouter() *Datarouter {
	return &Datarouter{}
}

func (router *Datarouter) HandlePriorSync(req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage {
	// Extract state from metadata
	if req.Metadata == nil {
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.PriorSyncAck{
				State: "UNKNOWN",
				Ok:    false,
				Error: "metadata is required",
			},
		}
	}

	state := req.Metadata.State

	// Route based on state
	switch state {
	case "SYNC_REQUEST":
		fmt.Println("Sync Request - LOG")
		// TODO: Implement sync request logic
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: state, Ok: true, Error: ""}}

	case "CHECKPOINT":
		fmt.Println("Checkpoint - LOG")
		// TODO: Implement checkpoint logic
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: state, Ok: true, Error: ""}}

	case "RECONCILE":
		fmt.Println("Reconcile - LOG")
		// TODO: Implement reconcile logic
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: state, Ok: true, Error: ""}}

	default:
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.PriorSyncAck{
				State: state,
				Ok:    false,
				Error: "unknown state: " + state,
			},
		}
	}
}
