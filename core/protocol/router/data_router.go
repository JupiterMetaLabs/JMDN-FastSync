package router

import (
	"bytes"
	"context"
	"fmt"

	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum/checksum_priorsync"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/errors"
)
const(
	namedlogger = "log:datarouter"
)

type Datarouter struct {
	Nodeinfo *types.Nodeinfo
}

func NewDatarouter(nodeinfo *types.Nodeinfo) *Datarouter {
	return &Datarouter{Nodeinfo: nodeinfo}
}

func (router *Datarouter) HandlePriorSync(ctx context.Context, req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage {
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
		Log.Logger(namedlogger).Debug(ctx, "Sync Request - LOG",
		ion.String("state", state),
		ion.String("function", "HandlePriorSync"))
		return router.SYNC_REQUEST(ctx, req)

	case "CHECKPOINT":
		Log.Logger(namedlogger).Debug(ctx, "Checkpoint - LOG",
		ion.String("state", state),
		ion.String("function", "HandlePriorSync"))
		
		// TODO: Implement checkpoint logic
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: state, Ok: true, Error: ""}}

	case "RECONCILE":
		Log.Logger(namedlogger).Debug(ctx, "Reconcile - LOG",
		ion.String("state", state),
		ion.String("function", "HandlePriorSync"))
		// TODO: Implement reconcile logic
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: state, Ok: true, Error: ""}}

	default:
		Log.Logger(namedlogger).Debug(ctx, "Unknown State - LOG",
		ion.String("state", state),
		ion.String("function", "HandlePriorSync"))
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

func (router *Datarouter) SYNC_REQUEST(ctx context.Context, req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage {
	/*
		- Check the checksum to make sure there is no data loss and message sent and received are same.
		- Load the latest block information from the node using the interface function.
		- Check if the user block and your block are on the same level. if yes then return message already on same level.
		- If the s-blockheight < c-blockheight - proceed to phase 2 sync and state SYNC_DATA by returning SYNC_REQUEST_RESPONSE then client will proceed from its side.
	*/
	verified, err := checksum_priorsync.PriorSyncChecksum().VerifyfromPB(req, uint16(req.Metadata.Version), req.Metadata.Checksum)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
		err,
		ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	if !verified {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
		errors.ChecksumMismatch,
		ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.ChecksumMismatch.Error()}}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		Log.Logger(namedlogger).Error(ctx, "BlockInfo is nil - LOG",
		errors.BlockInfoNil,
		ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.BlockInfoNil.Error()}}
	}
	blockNumber := blockInfo.GetBlockNumber()
	blockDetails := blockInfo.GetBlockDetails()

	msg := fmt.Sprintf("Block Details: %+v (StateRoot: %s, BlockHash: %s)", blockDetails, string(blockDetails.Stateroot), string(blockDetails.Blockhash))
	Log.Logger(namedlogger).Debug(ctx, msg,
		ion.String("function", "SYNC_REQUEST"))

	// 3. Check if the user block and your block are on the same level. if yes then return message already on same level.
	if blockNumber == req.Blocknumber {
		if !bytes.Equal(blockDetails.Stateroot, req.Stateroot) {
			Log.Logger(namedlogger).Error(ctx, "Stateroot Mismatch - LOG",
			errors.SameBlockHeight_DifferentStateroot,
			ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.SameBlockHeight_DifferentStateroot.Error()}}
		} else if !bytes.Equal(blockDetails.Blockhash, req.Blockhash) {
			Log.Logger(namedlogger).Error(ctx, "Blockhash Mismatch - LOG",
			errors.SameBlockHeight_DifferentBlockhash,
			ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.SameBlockHeight_DifferentBlockhash.Error()}}
		} else {
			Log.Logger(namedlogger).Warn(ctx, "Same Block Height - LOG",
			ion.Err(errors.SameBlockHeight),
			ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: errors.SameBlockHeight.Error()}}
		}
	}else if blockNumber > req.Blocknumber {
		// If the current node block height is higger than the node from which the request is coming, 
		// then return the message that the current node is already on the same block height.
		// Note that there is a thin possibility that inbetween blocks might be missing. for this we need to compute the merkle tree of all the blocks and then continue iterating
		// For now we mark it as TODO
		Log.Logger(namedlogger).Warn(ctx, "Block Height Higher - LOG",
			ion.Err(errors.BlockHeightHigher),
			ion.String("Current Block Number", fmt.Sprintf("%d", blockNumber)),
			ion.String("Provider Block Number", fmt.Sprintf("%d", req.Blocknumber)),
			ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: errors.BlockHeightHigher.Error()}}
	}

	// If the current node block height is less than the provider node then we need to sync the blocks from the provider node.
	// We sync only headers in this phase 2 so that we match up the blocks. in the follwoing phase 3 we do parallel sync the transactions and other data of all the blocks.
	// Now no need to think about the phase 3.

	// Build the struct of the current node block
	response := &priorsyncpb.PriorSync{
		Blocknumber: blockNumber,
		Stateroot:   blockDetails.Stateroot,
		Blockhash:   blockDetails.Blockhash,
		Metadata: &priorsyncpb.Metadata{
			Version: uint32(req.Metadata.Version),
			State:   constants.SYNC_REQUEST_RESPONSE,
		},
	}
	checksum, err := checksum_priorsync.PriorSyncChecksum().CreatefromPB(response, uint16(req.Metadata.Version))
	if err != nil {
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.PriorSyncAck{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	response.Metadata.Checksum = checksum

	return &priorsyncpb.PriorSyncMessage{
		Priorsync: response,
		Ack: &ackpb.PriorSyncAck{
			State: constants.SYNC_REQUEST_RESPONSE,
			Ok:    true,
			Error: "",
		},
	}
}
