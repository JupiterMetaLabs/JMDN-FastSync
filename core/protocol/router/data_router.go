package router

import (
	"bytes"
	"context"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	merkle_types "github.com/JupiterMetaLabs/JMDN-FastSync/helper/merkle"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum/checksum_priorsync"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/ack"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/block"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/headersync"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/errors"
)

const (
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
			Ack: &ackpb.Ack{
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
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: state, Ok: true, Error: ""}}

	case "RECONCILE":
		Log.Logger(namedlogger).Debug(ctx, "Reconcile - LOG",
			ion.String("state", state),
			ion.String("function", "HandlePriorSync"))
		// TODO: Implement reconcile logic
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: state, Ok: true, Error: ""}}

	default:
		Log.Logger(namedlogger).Debug(ctx, "Unknown State - LOG",
			ion.String("state", state),
			ion.String("function", "HandlePriorSync"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				State: state,
				Ok:    false,
				Error: "unknown state: " + state,
			},
		}
	}
}

func (router *Datarouter) SYNC_REQUEST_V2(ctx context.Context, req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage {
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
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	if !verified {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
			errors.ChecksumMismatch,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.ChecksumMismatch.Error()}}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		Log.Logger(namedlogger).Error(ctx, "BlockInfo is nil - LOG",
			errors.BlockInfoNil,
			ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.BlockInfoNil.Error()}}
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

			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.SameBlockHeight_DifferentStateroot.Error()}}
		} else if !bytes.Equal(blockDetails.Blockhash, req.Blockhash) {
			Log.Logger(namedlogger).Error(ctx, "Blockhash Mismatch - LOG",
				errors.SameBlockHeight_DifferentBlockhash,
				ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.SameBlockHeight_DifferentBlockhash.Error()}}
		} else {
			Log.Logger(namedlogger).Warn(ctx, "Same Block Height - LOG",
				ion.Err(errors.SameBlockHeight),
				ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: errors.SameBlockHeight.Error()}}
		}
	} else if blockNumber > req.Blocknumber {
		// If the current node block height is higger than the node from which the request is coming,
		// then return the message that the current node is already on the same block height.
		// Note that there is a thin possibility that inbetween blocks might be missing. for this we need to compute the merkle tree of all the blocks and then continue iterating
		// For now we mark it as TODO
		Log.Logger(namedlogger).Warn(ctx, "Block Height Higher - LOG",
			ion.Err(errors.BlockHeightHigher),
			ion.String("Current Block Number", fmt.Sprintf("%d", blockNumber)),
			ion.String("Provider Block Number", fmt.Sprintf("%d", req.Blocknumber)),
			ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: errors.BlockHeightHigher.Error()}}
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
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	response.Metadata.Checksum = checksum

	return &priorsyncpb.PriorSyncMessage{
		Priorsync: response,
		Ack: &ackpb.Ack{
			State: constants.SYNC_REQUEST_RESPONSE,
			Ok:    true,
			Error: "",
		},
	}
}

func (router *Datarouter) SYNC_REQUEST(ctx context.Context, req *priorsyncpb.PriorSync) *priorsyncpb.PriorSyncMessage {

	/*
		- Check the checksum to make sure there is no data loss and message sent and received are same.
		- Load the latest block information from the node using the interface function.
		- Generate the merkle tree of the target machine by reconstrucitng the req.merklesnapshot using the merkle.ReconstructTree function.
		- Generate the merkle tree by calling the merkle.GenerateMerkleTree function (using the config of the target machine to have same tree structure) for the local node
		- Bisect the merkle tree to find the to be synched block range.
		- In bisection you would get the range of blocks which are invalid, call the server node to send that particular blocks as the merkletree snapshot.
		- Continue the bisection and tag the to be synched blocks. in that short range.
		- give the block numbers to the PHASE 2. to get synched.
		- Continue this tagging process for all batches in the leaf nodes one by one. so you have to sync leaf nodes from left side.
								 [root]
								/      \
							[root]    [root]
							/    \      /    \
						[root] [root] [root] [root]
						/ \    / \    / \    / \
					   L   L  L   L  L   L  L   L
					   [0 to 200] [201 to 400] [401 to 600] [601 to 800] - Blockmerge is 200 so each L have hash of 200 blocks.
	*/

	verified, err := checksum_priorsync.PriorSyncChecksum().VerifyfromPB(req, uint16(req.Metadata.Version), req.Metadata.Checksum)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	if !verified {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
			errors.ChecksumMismatch,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.ChecksumMismatch.Error()}}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		Log.Logger(namedlogger).Error(ctx, "BlockInfo is nil - LOG",
			errors.BlockInfoNil,
			ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: errors.BlockInfoNil.Error()}}
	}

	blockNumber := blockInfo.GetBlockNumber()
	blockDetails := blockInfo.GetBlockDetails()

	msg := fmt.Sprintf("Block Details of Block %d: %+v (StateRoot: %s, BlockHash: %s)", blockNumber, blockDetails, string(blockDetails.Stateroot), string(blockDetails.Blockhash))
	Log.Logger(namedlogger).Debug(ctx, msg,
		ion.String("function", "SYNC_REQUEST"))

	// Reconstruct the merkle tree of the target machine.
	merkle_obj := merkle.NewMerkleProof()
	target_snap := merkle_types.ProtoToMerkleSnapshot(req.Merklesnapshot)
	target_merkletree_pointer, err := merkle_obj.ReconstructTree(ctx, target_snap)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Reconstruction Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	// create the local merkle tree with the same config as the target machine.
	local_merkletree_pointer, err := merkle_obj.GenerateMerkleTreeWithConfig(ctx, int64(req.Range.Start), int64(req.Range.End), &target_snap.Config)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Generation Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	target_merkletree_root, err := target_merkletree_pointer.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Finalization Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	local_merkletree_root, err := local_merkletree_pointer.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Finalization Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	// compare the merkle tree roots. If both are same then both are in sync.
	if target_merkletree_root == local_merkletree_root {
		Log.Logger(namedlogger).Info(ctx, "Merkle Trees are same - LOG",
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: ""}}
	}

	// bisect the merkle tree to find the to be synched block range.
	start, bCount, err := target_merkletree_pointer.Bisect(local_merkletree_pointer)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Bisect Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: false, Error: err.Error()}}
	}

	Log.Logger(namedlogger).Info(ctx, "Bisect Success - LOG",
		ion.Int64("start", int64(start)),
		ion.Int64("bCount", int64(bCount)),
		ion.String("function", "SYNC_REQUEST"))
	
	// Do recursion until the root is same. because we are bisecting the tree and should sync the leaf nodes to be synched. To be synched blocks should be tagged rather than sync directly. 
	// once that is one we need to update the leaf and parent nodes. but shouldn't reflect the sibling nodes. time complexity is O(log n).

	return &priorsyncpb.PriorSyncMessage{Priorsync: req, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: ""}}
}

// This is the Phase2 function that will take the tagged blocks and send to the server node to get the block headers sync.
func (router *Datarouter) HeaderSync(ctx context.Context, req *headersyncpb.HeaderSyncRequest) *headersyncpb.HeaderSyncResponse {
	/*
		- This is the header sync. 
		- After bisecting the tree in phase 1 with recursion. we get the tagged blocks per cycle. 
		- This blocks are transmitted to the server node to get the block headers synced.
	*/
	return &headersyncpb.HeaderSyncResponse{Header: []*block.Header{}, Ack: &ackpb.Ack{State: constants.SYNC_REQUEST_RESPONSE, Ok: true, Error: ""}}
}