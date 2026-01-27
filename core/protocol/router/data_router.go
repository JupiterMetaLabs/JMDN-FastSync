package router

import (
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"google.golang.org/protobuf/proto"
)

type Datarouter struct {
	Nodeinfo *types.Nodeinfo
}

func NewDatarouter(nodeinfo *types.Nodeinfo) *Datarouter {
	return &Datarouter{Nodeinfo: nodeinfo}
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
	case constants.SYNC_REQUEST:
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

func (router *Datarouter) SYNC_REQUEST(req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage {
	/*
		- Check the checksum to make sure there is no data loss and message sent and received are same.
		- Load the latest block information from the node using the interface function.
		- Check if the user block and your block are on the same level. if yes then return message already on same level.
		- If the s-blockheight < c-blockheight - proceed to phase 2 sync and state SYNC_DATA by returning SYNC_REQUEST_RESPONSE then client will proceed from its side.
	*/

	// make data from req by stripping the metadata from it
	// Use the protobuf-generated type instead of custom types.PriorSync
	data := &priorsyncpb.PriorSync{
		Blocknumber: req.Blocknumber,
		Blockhash:   req.Blockhash,
		Stateroot:   req.Stateroot,
		Metadata:    nil, // Explicitly exclude metadata for checksum verification
	}

	// convert struct to byte
	databytes, err := proto.Marshal(data)
	if err != nil {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	// 1. Check if the checksum is valid or not
	verified, err := checksum.NewChecksum().Verify(databytes, uint16(req.Metadata.Version), req.Metadata.Checksum)
	if err != nil {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}
	if !verified {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: "checksum mismatch"}}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: "block info is nil"}}
	}
	_ = blockInfo.GetBlockNumber()  // TODO: Implement block comparison logic
	_ = blockInfo.GetBlockDetails() // TODO: Implement block details processing

	// 3. Check if the user block and your block are on the same level. if yes then return message already on same level.
	// TODO: Implement block level comparison and sync logic
	// if blockNumber == req.Blocknumber {
	// 	return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: "already on same level"}}
	// }

	// Temporary return - TODO: Implement full sync logic
	return &priorsyncpb.PriorSyncMessage{
		Priorsync: req,
		Ack: &ackpb.PriorSyncAck{
			State: constants.SYNC_REQUEST_RESPONSE,
			Ok:    true,
			Error: "",
		},
	}
}
