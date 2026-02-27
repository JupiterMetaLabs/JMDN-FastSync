package router

import (
	"bytes"
	"context"
	"fmt"
	"math"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	merkle "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	merkle_types "github.com/JupiterMetaLabs/JMDN-FastSync/helper/merkle"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/JupiterMetaLabs/ion"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/checksum/checksum_priorsync"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	headerpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	libp2p_peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const (
	namedlogger = "log:datarouter"
)

type Datarouter struct {
	Nodeinfo *types.Nodeinfo
	Comm     communication.Communicator
}

func NewDatarouter(nodeinfo *types.Nodeinfo, comm communication.Communicator) *Datarouter {
	return &Datarouter{
		Nodeinfo: nodeinfo,
		Comm:     comm,
	}
}

func (router *Datarouter) HandlePriorSync(ctx context.Context, req *priorsyncpb.PriorSyncMessage, remote *types.Nodeinfo) *priorsyncpb.PriorSyncMessage {
	// Extract state from metadata
	if req.Priorsync.Metadata == nil || req.Phase == nil {
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req.Priorsync,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.MetadataRequired.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.UNKNOWN,
				Success:         false,
				Error:           errors.MetadataRequired.Error(),
			},
		}
	}

	state := req.Phase.PresentPhase

	// Route based on state
	switch state {
	case constants.SYNC_REQUEST:
		Log.Logger(namedlogger).Debug(ctx, "Sync Request - LOG",
			ion.String("state", state),
			ion.String("function", "HandlePriorSync"))

		// Extract peer info from metadata if available
		var peerInfo types.Nodeinfo
		if req.Priorsync.Metadata != nil && req.Priorsync.Metadata.Nodeinfo != nil {
			pbNodeInfo := req.Priorsync.Metadata.Nodeinfo
			var maddrs []multiaddr.Multiaddr
			for _, maBytes := range pbNodeInfo.Multiaddrs {
				ma, err := multiaddr.NewMultiaddrBytes(maBytes)
				if err == nil {
					maddrs = append(maddrs, ma)
				}
			}

			pid, _ := libp2p_peer.IDFromBytes(pbNodeInfo.PeerId)

			peerInfo = types.Nodeinfo{
				PeerID:       pid,
				Multiaddr:    maddrs,
				Capabilities: pbNodeInfo.Capabilities,
				Version:      uint16(pbNodeInfo.Version),
			}
		}

		return router.SYNC_REQUEST(ctx, req.Priorsync, peerInfo, remote)

	case constants.SYNC_REQUEST_AUTOPROCEED:
		Log.Logger(namedlogger).Debug(ctx, "Sync Request Auto Proceed - LOG",
			ion.String("state", state),
			ion.String("function", "HandlePriorSync"))

		// Extract peer info from metadata if available
		var peerInfo types.Nodeinfo
		if req.Priorsync.Metadata != nil && req.Priorsync.Metadata.Nodeinfo != nil {
			pbNodeInfo := req.Priorsync.Metadata.Nodeinfo
			var maddrs []multiaddr.Multiaddr
			for _, maBytes := range pbNodeInfo.Multiaddrs {
				ma, err := multiaddr.NewMultiaddrBytes(maBytes)
				if err == nil {
					maddrs = append(maddrs, ma)
				}
			}

			pid, _ := libp2p_peer.IDFromBytes(pbNodeInfo.PeerId)

			peerInfo = types.Nodeinfo{
				PeerID:       pid,
				Multiaddr:    maddrs,
				Capabilities: pbNodeInfo.Capabilities,
				Version:      uint16(pbNodeInfo.Version),
			}
		}

		return router.SYNC_FULL_AUTO(ctx, req.Priorsync, peerInfo, remote)

	default:
		Log.Logger(namedlogger).Debug(ctx, "Unknown State - LOG",
			ion.String("state", state),
			ion.String("function", "HandlePriorSync"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req.Priorsync,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: "unknown state: " + state,
			},
			Phase: &phasepb.Phase{
				PresentPhase:    state,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           "unknown state: " + state,
			},
		}
	}
}

func (router *Datarouter) HandleMerkle(ctx context.Context, merkleReq *merklepb.MerkleRequestMessage, remote *types.Nodeinfo) *merklepb.MerkleMessage {
	if merkleReq == nil || merkleReq.Request == nil {
		return &merklepb.MerkleMessage{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: "Merkle request or range is nil",
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.REQUEST_MERKLE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           "Merkle request or range is nil",
			},
		}
	}

	merkleRange := &merklepb.Range{
		Start: merkleReq.Request.Start,
		End:   merkleReq.Request.End,
	}

	// Pass the requester's config so we build the tree with the same BlockMerge.
	// If nil, REQUEST_MERKLE falls back to a default calculation.
	return router.REQUEST_MERKLE(ctx, merkleRange, merkleReq.Request.Config, remote)
}

func (router *Datarouter) HandleDataSync(ctx context.Context, req *datasyncpb.DataSyncRequest, remote *types.Nodeinfo) *datasyncpb.DataSyncResponse {
	// TODO: Implement DataSync
	return nil
}

func (router *Datarouter) HandleHeaderSync(ctx context.Context, headerSyncReq *headerpb.HeaderSyncRequest) *headerpb.HeaderSyncResponse {
	switch headerSyncReq.Phase.PresentPhase {
	case constants.HEADER_SYNC_REQUEST:
		if headerSyncReq == nil || headerSyncReq.Tag == nil {
			return &headerpb.HeaderSyncResponse{
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: "Header sync request or range is nil",
				},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.HEADER_SYNC_REQUEST,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           "Header sync request or range is nil",
				},
			}
		}
		return router.HeaderSync(ctx, headerSyncReq)

	default:
		return &headerpb.HeaderSyncResponse{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: "unknown state: " + headerSyncReq.Phase.PresentPhase,
			},
			Phase: &phasepb.Phase{
				PresentPhase:    headerSyncReq.Phase.PresentPhase,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           "unknown state: " + headerSyncReq.Phase.PresentPhase,
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
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	if !verified {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
			errors.ChecksumMismatch,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.ChecksumMismatch.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.ChecksumMismatch.Error(),
			},
		}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		Log.Logger(namedlogger).Error(ctx, "BlockInfo is nil - LOG",
			errors.BlockInfoNil,
			ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.BlockInfoNil.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.BlockInfoNil.Error(),
			},
		}
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

			return &priorsyncpb.PriorSyncMessage{
				Priorsync: req,
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: errors.SameBlockHeight_DifferentStateroot.Error()},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           errors.SameBlockHeight_DifferentStateroot.Error(),
				},
			}

		} else if !bytes.Equal(blockDetails.Blockhash, req.Blockhash) {
			Log.Logger(namedlogger).Error(ctx, "Blockhash Mismatch - LOG",
				errors.SameBlockHeight_DifferentBlockhash,
				ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{
				Priorsync: req,
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: errors.SameBlockHeight_DifferentBlockhash.Error()},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           errors.SameBlockHeight_DifferentBlockhash.Error(),
				},
			}

		} else {
			Log.Logger(namedlogger).Warn(ctx, "Same Block Height - LOG",
				ion.Err(errors.SameBlockHeight),
				ion.String("function", "SYNC_REQUEST"))

			return &priorsyncpb.PriorSyncMessage{
				Priorsync: req,
				Ack: &ackpb.Ack{
					Ok:    true,
					Error: errors.SameBlockHeight.Error()},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         true,
					Error:           errors.SameBlockHeight.Error(),
				},
			}

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

		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    true,
				Error: errors.BlockHeightHigher.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         true,
				Error:           errors.BlockHeightHigher.Error(),
			},
		}
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
		},
	}
	checksum, err := checksum_priorsync.PriorSyncChecksum().CreatefromPB(response, uint16(req.Metadata.Version))
	if err != nil {
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	response.Metadata.Checksum = checksum

	return &priorsyncpb.PriorSyncMessage{
		Priorsync: response,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
			SuccessivePhase: constants.HEADER_SYNC_REQUEST,
			Success:         true,
			Error:           "",
		},
	}
}

func (router *Datarouter) SYNC_REQUEST(ctx context.Context, req *priorsyncpb.PriorSync, peerNode types.Nodeinfo, remote *types.Nodeinfo) *priorsyncpb.PriorSyncMessage {

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
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	if !verified {
		Log.Logger(namedlogger).Error(ctx, "Checksum Verification Failed - LOG",
			errors.ChecksumMismatch,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.ChecksumMismatch.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.ChecksumMismatch.Error(),
			},
		}
	}

	// 2. Load the latest block information from the node using the interface function.
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		Log.Logger(namedlogger).Error(ctx, "BlockInfo is nil - LOG",
			errors.BlockInfoNil,
			ion.String("function", "SYNC_REQUEST"))

		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.BlockInfoNil.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.BlockInfoNil.Error(),
			},
		}
	}

	blockNumber := blockInfo.GetBlockNumber()
	blockDetails := blockInfo.GetBlockDetails()

	msg := fmt.Sprintf("Block Details of Block %d: %+v (StateRoot: %s, BlockHash: %s)", blockNumber, blockDetails, string(blockDetails.Stateroot), string(blockDetails.Blockhash))
	Log.Logger(namedlogger).Debug(ctx, msg,
		ion.String("function", "SYNC_REQUEST"))

	// Reconstruct the merkle tree of the target machine.
	merkle_obj := merkle.NewMerkleProof(blockInfo)
	target_snap := merkle_types.ProtoToMerkleSnapshot(req.Merklesnapshot)
	target_merkletree_pointer, err := merkle_obj.ReconstructTree(ctx, target_snap)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Reconstruction Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	// Guard against nil Range — default to full block span when not provided.
	if req.Range == nil {
		req.Range = &merklepb.Range{
			Start: 0,
			End:   blockNumber,
		}
	} else if req.Range.End == math.MaxUint64 || req.Range.End > blockNumber {
		req.Range.End = blockNumber
	}

	// create the local merkle tree with the same config as the target machine.
	local_merkletree_pointer, err := merkle_obj.GenerateMerkleTreeWithConfig(ctx, req.Range.Start, req.Range.End, &target_snap.Config)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Generation Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	target_merkletree_root, err := target_merkletree_pointer.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Finalization Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	local_merkletree_root, err := local_merkletree_pointer.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Finalization Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	// compare the merkle tree roots. If both are same then both are in sync.
	if target_merkletree_root == local_merkletree_root {
		Log.Logger(namedlogger).Info(ctx, "Merkle Trees are same - LOG",
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    true,
				Error: ""},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.DATA_SYNC_REQUEST,
				Success:         true,
				Error:           "",
			},
			Headersync: &headersyncpb.HeaderSyncRequest{
				Tag: nil,
				Ack: &ackpb.Ack{
					Ok:    true,
					Error: "",
				},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.HEADER_SYNC_REQUEST,
					SuccessivePhase: constants.DATA_SYNC_REQUEST,
					Success:         true,
					Error:           "",
				},	
			},
		}
	}

	header_sync_req, err := router.dataBisect(ctx, local_merkletree_pointer, target_merkletree_pointer, remote)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Bisect Failed - LOG",
			err,
			ion.String("function", "SYNC_REQUEST"))
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	// Log bisection results safely (Tag.Range may be empty if only block-level tags exist).
	numRangeTags := len(header_sync_req.Tag.Range)
	numBlockTags := len(header_sync_req.Tag.BlockNumber)
	Log.Logger(namedlogger).Info(ctx, "Bisect Success - LOG",
		ion.Int("num_range_tags", numRangeTags),
		ion.Int("num_block_tags", numBlockTags),
		ion.String("function", "SYNC_REQUEST"))

	// >> Log tagged ranges and blocks - just for verbose logging

	for i, r := range header_sync_req.Tag.Range {
		Log.Logger(namedlogger).Info(ctx, "Tagged range",
			ion.Int("index", i),
			ion.Int64("start", int64(r.Start)),
			ion.Int64("end", int64(r.End)),
			ion.Int64("count", int64(r.End-r.Start+1)),
			ion.String("function", "SYNC_REQUEST"))
	}

	for i, bn := range header_sync_req.Tag.BlockNumber {
		Log.Logger(namedlogger).Info(ctx, "Tagged block",
			ion.Int("index", i),
			ion.Int64("block_number", int64(bn)),
			ion.String("function", "SYNC_REQUEST"))
	}

	// Headersync request
	header_sync_req_msg := &headersyncpb.HeaderSyncRequest{
		Tag: header_sync_req.Tag,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.HEADER_SYNC_REQUEST,
			SuccessivePhase: constants.HEADER_SYNC_RESPONSE,
			Success:         true,
			Error:           "",
		},
	}

	return &priorsyncpb.PriorSyncMessage{
		Priorsync: req,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.SYNC_REQUEST_RESPONSE,
			SuccessivePhase: constants.HEADER_SYNC_REQUEST,
			Success:         true,
			Error:           "",
		},
		Headersync: header_sync_req_msg,
	}
}

// REQUEST_MERKLE constructs a merkle tree for the given range and returns it as a snapshot.
// If reqConfig is provided (non-nil with BlockMerge > 0), it is used for tree construction.
// Otherwise a default config is calculated (5% of range as BlockMerge).
func (router *Datarouter) REQUEST_MERKLE(ctx context.Context, Range *merklepb.Range, reqConfig *merklepb.SnapshotConfig, remote *types.Nodeinfo) *merklepb.MerkleMessage {

	var cfg merkletree.SnapshotConfig
	if reqConfig != nil && reqConfig.BlockMerge > 0 {
		// Use the requester's config so both sides build structurally identical trees.
		cfg = merkletree.SnapshotConfig{
			BlockMerge:    int(reqConfig.BlockMerge),
			ExpectedTotal: reqConfig.ExpectedTotal,
		}
	} else {
		// Default: 5% of range size as BlockMerge.
		totalBlocks := Range.End - Range.Start + 1
		cfg = merkletree.SnapshotConfig{
			BlockMerge:    int(math.Ceil(float64(totalBlocks) * 0.05)),
			ExpectedTotal: totalBlocks,
		}
	}

	// Build the tree with the given config and return back to the requested node as merkle tree snapshot
	if router.Nodeinfo == nil || router.Nodeinfo.BlockInfo == nil {
		err := fmt.Errorf("nodeinfo or blockinfo is nil")
		Log.Logger(namedlogger).Error(ctx, "Nodeinfo or BlockInfo is nil - LOG",
			err,
			ion.String("function", "REQUEST_MERKLE"))
		return &merklepb.MerkleMessage{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.REQUEST_MERKLE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	merkle_obj := merkle.NewMerkleProof(router.Nodeinfo.BlockInfo)

	snapshot_obj, err := merkle_obj.GenerateMerkleTreeWithConfig(ctx, Range.Start, Range.End, &cfg)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Generation Failed - LOG",
			err,
			ion.String("function", "REQUEST_MERKLE"))
		return &merklepb.MerkleMessage{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.REQUEST_MERKLE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	snapshot, err := merkle_obj.ToSnapshot(ctx, snapshot_obj)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Merkle Tree Snapshot Conversion Failed - LOG",
			err,
			ion.String("function", "REQUEST_MERKLE"))
		return &merklepb.MerkleMessage{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.REQUEST_MERKLE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	return &merklepb.MerkleMessage{
		Snapshot: snapshot,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.REQUEST_MERKLE,
			SuccessivePhase: constants.RESPONSE_MERKLE,
			Success:         true,
			Error:           "",
		},
	}
}

func (router *Datarouter) SYNC_FULL_AUTO(ctx context.Context, req *priorsyncpb.PriorSync, peerNode types.Nodeinfo, remote *types.Nodeinfo) *priorsyncpb.PriorSyncMessage {
	/*
		1. First do the SYNC_REQUEST
		- We can get the priorsyncmessage, take headersync request from the req.Headersync
		2. Then do the HEADER_SYNC, with the range and everything.
		- We can get the headersync response from the req.Headersync
		3. Then do the SYNC_RESPONSE
	*/

	return nil
}

// This is the Phase2 function that will take the tagged blocks and send to the server node to get the block headers sync.
func (router *Datarouter) HeaderSync(ctx context.Context, req *headersyncpb.HeaderSyncRequest) *headersyncpb.HeaderSyncResponse {
	/*
		- This is the header sync.
		- After bisecting the tree in phase 1 with recursion. we get the tagged blocks per cycle.
		- This blocks are transmitted to the server node to get the block headers synced.
	*/

	/*
		- get the headers of the blocks in the req.block_number slice.
		- then get the range headers from the req.range slice.
		- then send the all headers to the server node in sorted order.
	*/

	// Check for nil BlockInfo to prevent panic
	if router.Nodeinfo == nil || router.Nodeinfo.BlockInfo == nil {
		err := fmt.Errorf("nodeinfo or blockinfo is nil")
		Log.Logger(namedlogger).Error(ctx, "Nodeinfo or BlockInfo is nil - LOG",
			err,
			ion.String("function", "HEADER_SYNC"))
		return &headersyncpb.HeaderSyncResponse{
			Header: []*block.Header{},
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.HEADER_SYNC_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	all_headers := []*block.Header{}

	Headers_iterator := router.Nodeinfo.BlockInfo.NewBlockHeaderIterator()

	headers, err := Headers_iterator.GetBlockHeaders(req.Tag.BlockNumber)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "BlockHeaderIterator Creation Failed - LOG",
			err,
			ion.String("function", "HEADER_SYNC"))
		return &headersyncpb.HeaderSyncResponse{
			Header: []*block.Header{},
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error()},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.HEADER_SYNC_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	all_headers = append(all_headers, headers...)

	for i := range req.Tag.Range {
		headers, err := Headers_iterator.GetBlockHeadersRange(req.Tag.Range[i].Start, req.Tag.Range[i].End)
		if err != nil {
			Log.Logger(namedlogger).Error(ctx, "BlockHeaderIterator Creation Failed - LOG",
				err,
				ion.String("function", "HEADER_SYNC"))
			return &headersyncpb.HeaderSyncResponse{
				Header: []*block.Header{},
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: err.Error()},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.HEADER_SYNC_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           err.Error(),
				},
			}
		}

		all_headers = append(all_headers, headers...)
	}

	return &headersyncpb.HeaderSyncResponse{
		Header: all_headers,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: ""},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.HEADER_SYNC_RESPONSE,
			SuccessivePhase: constants.MERGE_REQUEST,
			Success:         true,
			Error:           "",
		},
	}
}

// func (router *Datarouter) DataSync(ctx context.Context, req *datasyncpb.DataSyncRequest) *datasyncpb.DataSyncResponse {
// }