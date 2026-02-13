package router

import (
	"context"

	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
)

func (router *Datarouter) HandleStreamMessage(ctx context.Context, req *priorsyncpb.StreamMessage) *priorsyncpb.StreamMessage {

	switch payload := req.Payload.(type) {
	case *priorsyncpb.StreamMessage_PriorSync:
		// Delegate to existing logic
		resp := router.HandlePriorSync(ctx, payload.PriorSync)
		return &priorsyncpb.StreamMessage{
			Payload: &priorsyncpb.StreamMessage_PriorSync{
				PriorSync: resp,
			},
		}

	case *priorsyncpb.StreamMessage_MerkleReq:
		return router.HandleMerkle(ctx, payload.MerkleReq)

	default:
		return &priorsyncpb.StreamMessage{
			Payload: &priorsyncpb.StreamMessage_PriorSync{
				PriorSync: &priorsyncpb.PriorSyncMessage{
					Ack: &ackpb.Ack{
						Ok:    false,
						Error: "unknown message type",
					},
					Phase: &phasepb.Phase{
						PresentPhase:    constants.UNKNOWN,
						SuccessivePhase: constants.FAILURE,
						Success:         false,
						Error:           "unknown message type",
					},
				},
			},
		}
	}
}
