package router

import (
	"fmt"

	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
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
	case constants.SYNC_REQUEST	:
		return router.SYNC_REQUEST(req)
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

func (router *Datarouter) SYNC_REQUEST(req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage{
	/*
		- Check the checksum to make sure there is no data loss and message sent and received are same. 
		- Load the latest block information from the node using the interface function.
		- Check if the user block and your block are on the same level. if yes then return message already on same level.
		- If the s-blockheight < c-blockheight - proceed to phase 2 sync and state SYNC_DATA by returning SYNC_REQUEST_RESPONSE then client will proceed from its side.
	*/
	fmt.Println("Sync Request - LOG")
	fmt.Println("Blocknumber: ", req.Blocknumber)
	fmt.Println("Stateroot: ", req.Stateroot)
	fmt.Println("Blockhash: ", req.Blockhash)
	fmt.Println("Metadata: ", req.Metadata)

	// TODO: Implement sync request logic

	//Debugging
	sREQ := &priorsyncpb.PriorSync{
		Blocknumber: 0,
		Stateroot:   []byte("example-state-root"),
		Blockhash:   []byte("example-block-hash"),
		Metadata: &priorsyncpb.Metadata{
			Checksum: []byte("example-checksum"),
			State:    "SYNC_REQUEST_RESPONSE",
			Version:  1,
		},
	}

	return &priorsyncpb.PriorSyncMessage{Priorsync: sREQ, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: ""}}
}
	