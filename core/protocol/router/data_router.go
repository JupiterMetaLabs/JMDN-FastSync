package router

import (
	"bytes"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum/checksum_priorsync"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/errors"
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
				Error: errors.MetadataRequired.Error(),
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
	verified, err := checksum_priorsync.PriorSyncChecksum().VerifyfromPB(req, uint16(req.Metadata.Version), req.Metadata.Checksum)
	if err != nil {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}
	if !verified {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.ChecksumMismatch.Error()}}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.BlockInfoNil.Error()}}
	}
	blockNumber := blockInfo.GetBlockNumber()   // TODO: Implement block comparison logic
	blockDetails := blockInfo.GetBlockDetails() // TODO: Implement block details processing

	fmt.Println("Block Details: ", blockDetails)

	// 3. Check if the user block and your block are on the same level. if yes then return message already on same level.
	// TODO: Implement block level comparison and sync logic
	if blockNumber == req.Blocknumber {
		if !bytes.Equal(blockDetails.Stateroot, req.Stateroot) {
			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.SameBlockHeight_DifferentStateroot.Error()}}
		} else if !bytes.Equal(blockDetails.Blockhash, req.Blockhash) {
			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.SameBlockHeight_DifferentBlockhash.Error()}}
		} else {
			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: errors.SameBlockHeight.Error()}}
		}
	}

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
