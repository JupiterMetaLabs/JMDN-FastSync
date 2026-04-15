package router

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/checksum/checksum_priorsync"
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headerpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	nodeinfopb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/nodeinfo"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	merkle "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper"
	potshelper "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper/pots"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/tagging"
	merkle_types "github.com/JupiterMetaLabs/JMDN-FastSync/helper/merkle"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/JupiterMetaLabs/ion"
	libp2p_peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const (
	namedlogger = "log:datarouter"
)

type Datarouter struct {
	Nodeinfo             *types.Nodeinfo
	Comm                 communication.Communicator
	clientGeneratedUUID  string
	availabilityLimiters sync.Map // libp2p_peer.ID -> *rate.Limiter
}

func NewDatarouter(nodeinfo *types.Nodeinfo, comm communication.Communicator) *Datarouter {
	return &Datarouter{
		Nodeinfo:            nodeinfo,
		Comm:                comm,
		clientGeneratedUUID: "",
	}
}

// getAvailabilityLimiter returns the per-peer rate limiter for HandleAvailability,
// creating one on first call. Allows burst of 3 then 2 request per minute per peer.
func (router *Datarouter) getAvailabilityLimiter(peerID libp2p_peer.ID) *rate.Limiter {
	v, _ := router.availabilityLimiters.LoadOrStore(peerID, rate.NewLimiter(rate.Every(30*time.Second), 3))
	return v.(*rate.Limiter)
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
				Auth:            req.Phase.Auth,
			},
		}
	}

	// Authenticate the request
	if req.Phase.Auth == nil || req.Phase.Auth.UUID == "" {
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req.Priorsync,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthRequired.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.UNKNOWN,
				Success:         false,
				Error:           errors.AuthRequired.Error(),
				Auth:            req.Phase.Auth,
			},
		}
	}

	authenticated, err := router.Authenticate(ctx, req.Phase.Auth, remote)
	if err != nil || !authenticated {
		return &priorsyncpb.PriorSyncMessage{
			Priorsync: req.Priorsync,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthenticationFailed.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.AuthenticationFailed.Error(),
				Auth:            req.Phase.Auth,
			},
		}
	}

	// Defer TTL reset
	defer func() {
		if resetErr := router.ResetTTL(ctx, req.Phase.Auth, remote); resetErr != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to reset TTL", resetErr)
		}
	}()

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

		Data := router.SYNC_REQUEST(ctx, req.Priorsync, peerInfo, remote)
		if Data.Phase != nil {
			Data.Phase.Auth = req.Phase.Auth
		}
		if Data.Headersync != nil && Data.Headersync.Phase != nil {
			Data.Headersync.Phase.Auth = req.Phase.Auth
		}
		return Data

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
				Auth:            req.Phase.Auth,
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
				Auth:            merkleReq.Phase.Auth,
			},
		}
	}

	if merkleReq.Phase == nil || merkleReq.Phase.Auth == nil || merkleReq.Phase.Auth.UUID == "" {
		return &merklepb.MerkleMessage{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthRequired.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.REQUEST_MERKLE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.AuthRequired.Error(),
				Auth:            merkleReq.Phase.Auth,
			},
		}
	}

	authenticated, err := router.Authenticate(ctx, merkleReq.Phase.Auth, remote)
	if err != nil || !authenticated {
		return &merklepb.MerkleMessage{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthenticationFailed.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.REQUEST_MERKLE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.AuthenticationFailed.Error(),
				Auth:            merkleReq.Phase.Auth,
			},
		}
	}

	// Defer TTL reset
	defer func() {
		if resetErr := router.ResetTTL(ctx, merkleReq.Phase.Auth, remote); resetErr != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to reset TTL", resetErr)
		}
	}()

	merkleRange := &merklepb.Range{
		Start: merkleReq.Request.Start,
		End:   merkleReq.Request.End,
	}

	// Pass the requester's config so we build the tree with the same BlockMerge.
	// If nil, REQUEST_MERKLE falls back to a default calculation.
	Data := router.REQUEST_MERKLE(ctx, merkleRange, merkleReq.Request.Config, remote)
	Data.Phase.Auth = merkleReq.Phase.Auth
	return Data
}

func (router *Datarouter) HandleHeaderSync(ctx context.Context, headerSyncReq *headerpb.HeaderSyncRequest, remote *types.Nodeinfo) *headerpb.HeaderSyncResponse {
	// Authenticate the request
	if headerSyncReq.Phase.Auth == nil || headerSyncReq.Phase.Auth.UUID == "" {
		return &headerpb.HeaderSyncResponse{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthRequired.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.UNKNOWN,
				Success:         false,
				Error:           errors.AuthRequired.Error(),
				Auth:            headerSyncReq.Phase.Auth,
			},
		}
	}

	authenticated, err := router.Authenticate(ctx, headerSyncReq.Phase.Auth, remote)
	if err != nil || !authenticated {
		return &headerpb.HeaderSyncResponse{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthenticationFailed.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.AuthenticationFailed.Error(),
				Auth:            headerSyncReq.Phase.Auth,
			},
		}
	}

	// Defer TTL reset
	defer func() {
		if resetErr := router.ResetTTL(ctx, headerSyncReq.Phase.Auth, remote); resetErr != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to reset TTL", resetErr)
		}
	}()

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
					Auth:            headerSyncReq.Phase.Auth,
				},
			}
		}
		Data := router.HEADER_SYNC(ctx, headerSyncReq)
		Data.Phase.Auth = headerSyncReq.Phase.Auth
		return Data

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
				Auth:            headerSyncReq.Phase.Auth,
			},
		}
	}
}

func (router *Datarouter) HandleDataSync(ctx context.Context, req *datasyncpb.DataSyncRequest, remote *types.Nodeinfo) *datasyncpb.DataSyncResponse {
	// Authenticate the request
	if req.Phase.Auth == nil || req.Phase.Auth.UUID == "" {
		return &datasyncpb.DataSyncResponse{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthRequired.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.UNKNOWN,
				Success:         false,
				Error:           errors.AuthRequired.Error(),
				Auth:            req.Phase.Auth,
			},
		}
	}

	authenticated, err := router.Authenticate(ctx, req.Phase.Auth, remote)
	if err != nil || !authenticated {
		return &datasyncpb.DataSyncResponse{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: errors.AuthenticationFailed.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.UNKNOWN,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           errors.AuthenticationFailed.Error(),
				Auth:            req.Phase.Auth,
			},
		}
	}

	// Defer TTL reset
	defer func() {
		if resetErr := router.ResetTTL(ctx, req.Phase.Auth, remote); resetErr != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to reset TTL", resetErr)
		}
	}()

	switch req.Phase.PresentPhase {
	case constants.DATA_SYNC_REQUEST:
		if req == nil || req.Tag == nil {
			return &datasyncpb.DataSyncResponse{
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: "Data sync request or range is nil",
				},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.DATA_SYNC_REQUEST,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           "Data sync request or range is nil",
					Auth:            req.Phase.Auth,
				},
			}
		}
		Data := router.DATA_SYNC(ctx, req)
		Data.Phase.Auth = req.Phase.Auth
		return Data

	default:
		return &datasyncpb.DataSyncResponse{
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: "unknown state: " + req.Phase.PresentPhase,
			},
			Phase: &phasepb.Phase{
				PresentPhase:    req.Phase.PresentPhase,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           "unknown state: " + req.Phase.PresentPhase,
				Auth:            req.Phase.Auth,
			},
		}
	}
}

func (router *Datarouter) HandleAvailability(ctx context.Context, req *availabilitypb.AvailabilityRequest, remote *types.Nodeinfo) *availabilitypb.AvailabilityResponse {
	template := &availabilitypb.AvailabilityResponse{
		IsAvailable: false,
		Nodeinfo: &nodeinfopb.NodeInfo{

			Multiaddrs: make([][]byte, 0),
		},
		BlockMerge: 0,
		Auth: &authpb.Auth{
			UUID: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.AVAILABILITY_RESPONSE,
			SuccessivePhase: constants.FAILURE,
			Error:           "",
			Success:         false,
		},
	}

	if !router.getAvailabilityLimiter(remote.PeerID).Allow() {
		template.Phase.Error = errors.RateLimitExceeded.Error()
		return template
	}

	if req.Range == nil {
		template.Phase.Error = "range cannot be nil"
		return template
	}

	if len(router.Nodeinfo.Multiaddr) == 0 {
		template.Phase.Error = "No Multiaddress to connect"
		return template
	}

	fastsyncready := availability.FastsyncReady().AmIAvailable()
	if fastsyncready {

		loggerCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		var err error
		template.Nodeinfo, err = helper.NewNodeInfoHelper().ToProto(router.Nodeinfo)

		if err != nil {
			template.Phase.Error = err.Error()
			return template
		}

		// Calculate the blockmerge (current block height) of the present server
		blockmerge, err := merkle.NewMerkleProof(router.Nodeinfo.BlockInfo).GenerateMerkleConfig(loggerCtx, req.Range.Start, req.Range.End)
		if err != nil {
			template.Phase.Error = err.Error()
			return template
		}

		UUID := helper.GenerateUUID()

		// MAKE AUTH Request
		AUTH_err := router.Nodeinfo.BlockInfo.AUTH().AddRecord(remote.PeerID, UUID)
		if AUTH_err != nil {
			template.Phase.Error = AUTH_err.Error()
			return template
		}

		template.BlockMerge = uint32(blockmerge.BlockMerge)

		template.IsAvailable = true
		template.Phase.SuccessivePhase = constants.SYNC_REQUEST
		template.Phase.Success = true
		template.Auth.UUID = UUID
		template.Phase.Error = ""
		return template
	}

	template.Phase.Error = "Not available at this moment"
	return template
}

func (router *Datarouter) HandlePoTSync(ctx context.Context, req *potspb.PoTSRequest, remote *types.Nodeinfo) *potspb.PoTSResponse {
	template := &potspb.PoTSResponse{
		Tag:               nil,
		LatestBlockNumber: math.MaxUint64,
		Phase: &phasepb.Phase{
			PresentPhase:    constants.PoTS_RESPONSE,
			SuccessivePhase: constants.FAILURE,
			Success:         false,
			Error:           "",
			Auth:            nil,
		},
	}

	if req.Phase == nil || req.Phase.Auth == nil || req.Phase.Auth.UUID == "" {
		template.Phase.Error = errors.AuthRequired.Error()
		return template
	}

	// Authenticate the request
	authenticated, err := router.Authenticate(ctx, req.Phase.Auth, remote)
	if err != nil || !authenticated {
		template.Phase.Error = errors.AuthenticationFailed.Error()
		template.Phase.Auth = req.Phase.Auth
		return template
	}

	// Defer TTL reset
	defer func() {
		if resetErr := router.ResetTTL(ctx, req.Phase.Auth, remote); resetErr != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to reset TTL", resetErr)
		}
	}()

	serverLatestBlock := router.Nodeinfo.BlockInfo.GetBlockNumber()

	// Log the PoTS request
	Log.Logger(namedlogger).Info(ctx, "Processing PoTS request",
		ion.String("remote_peer", remote.PeerID.String()),
		ion.Uint64("client_latest_block", req.LatestBlockNumber),
		ion.Int("client_blocks_count", len(req.Blocks)))

	// Process PoTS request using the helper
	helper := potshelper.NewPoTSHelper(router.Nodeinfo)
	response, err := helper.ProcessPoTSRequest(req)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to process PoTS request", err)
		return &potspb.PoTSResponse{
			LatestBlockNumber: serverLatestBlock,
			Phase: &phasepb.Phase{
				PresentPhase:    constants.PoTS_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           fmt.Sprintf("failed to process PoTS request: %v", err),
				Auth:            req.Phase.Auth,
			},
		}
	}

	// Set auth in response phase
	if response.Phase != nil {
		response.Phase.Auth = req.Phase.Auth
	}

	// Log successful response
	tagInfo := ""
	if response.Tag != nil {
		tagInfo = fmt.Sprintf("ranges=%d, blocks=%d", len(response.Tag.Range), len(response.Tag.BlockNumber))
	}
	Log.Logger(namedlogger).Info(ctx, "PoTS request processed successfully",
		ion.String("remote_peer", remote.PeerID.String()),
		ion.Uint64("server_latest_block", response.LatestBlockNumber),
		ion.String("tags", tagInfo))

	return response
}

func (router *Datarouter) HandleAccountsSync(ctx context.Context, req *accountspb.AccountNonceSyncRequest, remote *types.Nodeinfo) *accountspb.AccountSyncEndOfStream {
	template := &accountspb.AccountSyncEndOfStream{
		TotalPages: 0,
		TotalAccounts: 0,
		Ack: &ackpb.Ack{
			Ok:    false,
			Error: errors.AuthRequired.Error(),
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
			SuccessivePhase: constants.FAILURE,
			Success:         false,
			Error:           errors.AuthRequired.Error(),
			Auth:            nil,
		},
	}

	if req.Phase == nil || req.Phase.Auth == nil || req.Phase.Auth.UUID == "" {
		template.Phase.Error = errors.AuthRequired.Error()
		return template
	}

	authenticated, err := router.Authenticate(ctx, req.Phase.Auth, remote)
	if err != nil || !authenticated {
		template.Phase.Error = errors.AuthenticationFailed.Error()
		template.Phase.Auth = req.Phase.Auth
		return template
	}

	// Defer TTL reset
	defer func() {
		if resetErr := router.ResetTTL(ctx, req.Phase.Auth, remote); resetErr != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to reset TTL", resetErr)
		}
	}()

	Log.Logger(namedlogger).Info(ctx, "Processing Accounts Sync Request",
		ion.String("remote_peer", remote.PeerID.String()),
		ion.Uint64("total_keys", req.TotalKeys),
		ion.Int("batch_index", int(req.BatchIndex)),
		ion.String("keys_range", req.KeysRange.String()),
		ion.String("phase", req.Phase.PresentPhase),
		ion.String("is_last", fmt.Sprintf("%t", req.IsLast)),
	)
	
	switch req.Phase.PresentPhase {
	case constants.ACCOUNTS_SYNC_REQUEST:
		if req == nil || req.KeysRange == nil {
			return &accountspb.AccountSyncEndOfStream{
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: errors.AccountsSyncRequestNil.Error(),
				},
			}
		}
		AccountsSync := router.ACCOUNTS_SYNC(ctx, req)
		AccountsSync.Phase.Auth = req.Phase.Auth
		return AccountsSync

	default:
		return &accountspb.AccountSyncEndOfStream{
			TotalPages: 0,
			TotalAccounts: 0,
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: "unknown state: " + req.Phase.PresentPhase,
			},
			Phase: &phasepb.Phase{
				PresentPhase:    req.Phase.PresentPhase,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           "unknown state: " + req.Phase.PresentPhase,
				Auth:            req.Phase.Auth,
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

	/*
	   if the number of blocks in the client is less than MIN_BLOCKS then do the full sync.
	   if number of blocks is above MIN_BLOCKS and Server blocks - MINBLOCKS >= MINBLOCKS then we are doing the full sync.
	   if MIN_BLOCKS is 1000 then we are doing full sync only if client have less than 1000 blocks and server have more than 2000 blocks.
	*/
	if req.Blocknumber <= constants.MIN_BLOCKS && blockNumber-req.Blocknumber >= constants.MIN_BLOCKS {
		Log.Logger(namedlogger).Debug(ctx, "Block number is less than MIN_BLOCKS, doing full sync",
			ion.String("function", "SYNC_REQUEST"))
		return router.FULL_SYNC(ctx, req, peerNode, blockNumber, remote)
	}

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
	Log.Logger(namedlogger).Debug(ctx, "Bisect Success - LOG",
		ion.Int("num_range_tags", numRangeTags),
		ion.Int("num_block_tags", numBlockTags),
		ion.String("function", "SYNC_REQUEST"))

	// >> Log tagged ranges and blocks - just for verbose logging

	// for i, r := range header_sync_req.Tag.Range {
	// 	Log.Logger(namedlogger).Info(ctx, "Tagged range",
	// 		ion.Int("index", i),
	// 		ion.Int64("start", int64(r.Start)),
	// 		ion.Int64("end", int64(r.End)),
	// 		ion.Int64("count", int64(r.End-r.Start+1)),
	// 		ion.String("function", "SYNC_REQUEST"))
	// }

	// for i, bn := range header_sync_req.Tag.BlockNumber {
	// 	Log.Logger(namedlogger).Info(ctx, "Tagged block",
	// 		ion.Int("index", i),
	// 		ion.Int64("block_number", int64(bn)),
	// 		ion.String("function", "SYNC_REQUEST"))
	// }

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

// This is the Phase2 function that will take the tagged blocks and send to the server node to get the block headers sync.
func (router *Datarouter) HEADER_SYNC(ctx context.Context, req *headersyncpb.HeaderSyncRequest) *headersyncpb.HeaderSyncResponse {
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

func (router *Datarouter) DATA_SYNC(ctx context.Context, req *datasyncpb.DataSyncRequest) *datasyncpb.DataSyncResponse {
	/*
		- This is the data sync.
		- After bisecting the tree in phase 1 with recursion followed by the headersync request. we get the tagged blocks per cycle for non header sync. (headers would be synced at the headersync stage)
		- This blocks with no headers are transmitted to the requested client node to get the block non headers synced.
	*/

	// Validate request
	if req == nil || req.Tag == nil {
		err := fmt.Errorf("datasync request or tag is nil")
		Log.Logger(namedlogger).Error(ctx, "DataSync request is nil - LOG",
			err,
			ion.String("function", "DATA_SYNC"))
		return &datasyncpb.DataSyncResponse{
			Data: []*block.NonHeaders{},
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.DATA_SYNC_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	// Check for nil BlockInfo to prevent panic
	if router.Nodeinfo == nil || router.Nodeinfo.BlockInfo == nil {
		err := fmt.Errorf("nodeinfo or blockinfo is nil")
		Log.Logger(namedlogger).Error(ctx, "Nodeinfo or BlockInfo is nil - LOG",
			err,
			ion.String("function", "DATA_SYNC"))
		return &datasyncpb.DataSyncResponse{
			Data: []*block.NonHeaders{},
			Ack: &ackpb.Ack{
				Ok:    false,
				Error: err.Error(),
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.DATA_SYNC_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           err.Error(),
			},
		}
	}

	allNonHeaders := []*block.NonHeaders{}

	nonHeaderIterator := router.Nodeinfo.BlockInfo.NewBlockNonHeaderIterator()

	// Fetch non-headers for individual block numbers
	if len(req.Tag.BlockNumber) > 0 {
		nonHeaders, err := nonHeaderIterator.GetBlockNonHeaders(req.Tag.BlockNumber)
		if err != nil {
			Log.Logger(namedlogger).Error(ctx, "GetBlockNonHeaders failed - LOG",
				err,
				ion.String("function", "DATA_SYNC"))
			return &datasyncpb.DataSyncResponse{
				Data: []*block.NonHeaders{},
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: err.Error(),
				},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.DATA_SYNC_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           err.Error(),
				},
			}
		}
		allNonHeaders = append(allNonHeaders, nonHeaders...)
	}

	// Fetch non-headers for each range
	for i := range req.Tag.Range {
		nonHeaders, err := nonHeaderIterator.GetBlockNonHeadersRange(req.Tag.Range[i].Start, req.Tag.Range[i].End)
		if err != nil {
			Log.Logger(namedlogger).Error(ctx, "GetBlockNonHeadersRange failed - LOG",
				err,
				ion.String("function", "DATA_SYNC"),
				ion.Uint64("range_start", req.Tag.Range[i].Start),
				ion.Uint64("range_end", req.Tag.Range[i].End))
			return &datasyncpb.DataSyncResponse{
				Data: []*block.NonHeaders{},
				Ack: &ackpb.Ack{
					Ok:    false,
					Error: err.Error(),
				},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.DATA_SYNC_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           err.Error(),
				},
			}
		}
		allNonHeaders = append(allNonHeaders, nonHeaders...)
	}

	Log.Logger(namedlogger).Info(ctx, "DataSync completed",
		ion.String("function", "DATA_SYNC"),
		ion.Int("total_nonheaders", len(allNonHeaders)))

	tagger := tagging.NewTagging()
	for _, nonHeader := range allNonHeaders {
		for _, tx := range nonHeader.Transactions {
			if tx.Tx != nil {
				if len(tx.Tx.From) > 0 {
					tagger.TagAccounts(hex.EncodeToString(tx.Tx.From))
				}
				if len(tx.Tx.To) > 0 {
					tagger.TagAccounts(hex.EncodeToString(tx.Tx.To))
				}
			}
		}
	}

	return &datasyncpb.DataSyncResponse{
		Data:           allNonHeaders,
		Taggedaccounts: tagger.GetAccountTag(),
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.DATA_SYNC_RESPONSE,
			SuccessivePhase: constants.MERGE_REQUEST,
			Success:         true,
			Error:           "",
		},
	}
}

func (router *Datarouter) FULL_SYNC(ctx context.Context, req *priorsyncpb.PriorSync, peerNode types.Nodeinfo, blocknumber uint64, remote *types.Nodeinfo) *priorsyncpb.PriorSyncMessage {
	// you have to tag the blocks numbers range
	tags := helper.DivideTags(req.Blocknumber, blocknumber)
	Log.Logger(namedlogger).Debug(ctx, "Tagged Blocks - LOG",
		ion.String("function", "FullSync"),
		ion.Int64("blocknumber", int64(blocknumber)),
		ion.Int64("req.Blocknumber", int64(req.Blocknumber)))

	return &priorsyncpb.PriorSyncMessage{
		Priorsync: req,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.FULL_SYNC_REQUEST,
			SuccessivePhase: constants.HEADER_SYNC_REQUEST,
			Success:         true,
			Error:           "",
		},
		Headersync: &headersyncpb.HeaderSyncRequest{
			Tag: tags,
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
		},
	}
}

func(router *Datarouter) ACCOUNTS_SYNC(ctx context.Context, req *accountspb.AccountNonceSyncRequest) *accountspb.AccountSyncEndOfStream {
	template := &accountspb.AccountSyncEndOfStream{
		TotalPages: 0,
		TotalAccounts: 0,
		Ack: &ackpb.Ack{
			Ok:    false,
			Error: errors.AccountsSyncArtNil.Error(),
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
			SuccessivePhase: constants.FAILURE,
			Success:         false,
			Error:           errors.AccountsSyncRequestNil.Error(),
		},
	}
	/*
		- This is the accounts sync.
		- Before the Headersync request, we need to get the accounts sync.
		- The client would send the accounts of its local commit as the ART chunk.
			* If the client have sent the ART chunk, with is_last set to true, then the server would start the diff computation.
			* If the client have sent the ART chunk, with is_last set to false, then the server would merge the ART chunk with the client's global ART chunk. and delete the new received chunk. (we need to maintain the only one global ART of a client)
			* Beyond the window of the ART code would automatically swap to the disk to avoid memory exhaustion.
		- After getting the ART of whole client's data, the server would start the diff computation.
		- Server will collect the did's whose nonces are not in global art, and send the accounts of these did's to the client.
		- Note that we not gonna send every did in each response, we will send it by batches in the parallel responses. No ordering is required for the accounts sync.
	*/
	if req == nil || req.Art == nil {
		return template
	}

	switch req.IsLast{
	case true:
		// Start the diff computation.

	case false:	
		/*
			1. ART sent would be compressed with zstd. so Need to be decompressed first.
			2. Merge the ART chunk with the client's global ART chunk. and delete the new received chunk.
			3. Delete the new received chunk.
			4. Swapping to the disk is done automatically by the ART code.
		*/
		chunkART, err := art.Decode(req.GetArt())
		if err != nil {
			template.Ack.Error = err.Error()
			return template
		}

		err = router.Nodeinfo.ART.Merge(chunkART)
		if err != nil {
			template.Ack.Error = err.Error()
			return template
		}
		
		template.Ack.Ok = true
		template.Ack.Error = ""
		template.Phase.PresentPhase = constants.ACCOUNTS_SYNC_RESPONSE
		// if isLast is false then SuccessivePhase would be the current phase
		template.Phase.SuccessivePhase = constants.ACCOUNTS_SYNC_REQUEST
		template.Phase.Success = true
		template.Phase.Error = ""
		return template
	}

	return template
}