package priorsync

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/checksum/checksum_priorsync"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	sync_proto "github.com/JupiterMetaLabs/JMDN-FastSync/core/sync"
	priorsync_converter "github.com/JupiterMetaLabs/JMDN-FastSync/helper/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
)

type PriorSync struct {
	PriorSync_msg *types.PriorSyncMessage
	SyncVars      *types.Syncvars

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewPriorSyncRouter() Priorsync_router {
	return &PriorSync{
		PriorSync_msg: &types.PriorSyncMessage{},
		SyncVars:      &types.Syncvars{},
		mu:            sync.Mutex{},
	}
}

func (ps *PriorSync) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) Priorsync_router {
	if ps.SyncVars == nil {
		ps.SyncVars = &types.Syncvars{}
	}

	// Debugging
	logging.Logger(logging.PriorSync).Info(ctx, "Setting sync vars",
		ion.Int64("protocolVersion", int64(protocolVersion)),
		ion.String("peerID", nodeInfo.PeerID.String()),
		ion.String("multiaddr", nodeInfo.Multiaddr[0].String()),
	)

	ps.SyncVars.Version = protocolVersion
	ps.SyncVars.NodeInfo = nodeInfo
	ps.SyncVars.Ctx = ctx
	ps.SyncVars.WAL = wal
	ps.SyncVars.Node = node
	return ps
}

func (ps *PriorSync) GetSyncVars() *types.Syncvars {
	return ps.SyncVars
}

func (ps *PriorSync) SetupNetworkHandlers(debug bool) error {
	if ps.SyncVars.Node == nil {
		return errors.New("host is nil")
	}
	if ps.SyncVars == nil || ps.SyncVars.Ctx == nil {
		return errors.New("sync vars ctx not set")
	}
	if !availability.FastsyncReady().AmIAvailable(){
		return errors.New("not available right now")
	}

	// derive child from parent; child cannot outlive parent
	ctx, cancel := context.WithCancel(ps.SyncVars.Ctx)

	// Initialize Communication
	comm := communication.NewCommunication(ps.SyncVars.Node, ps.SyncVars.Version)

	// Initialize Sync Handler (Builder Pattern)
	syncHandler := sync_proto.NewSyncHandler(&ps.SyncVars.NodeInfo, comm, debug)

	ps.mu.Lock()

	// If called twice, stop the old one first
	var RemoveStreams func()
	RemoveStreams = func() {
		ps.SyncVars.Node.RemoveStreamHandler(constants.PriorSyncProtocol)
		ps.SyncVars.Node.RemoveStreamHandler(constants.MerkleProtocol)
		ps.SyncVars.Node.RemoveStreamHandler(constants.HeaderSyncProtocol)
		ps.SyncVars.Node.RemoveStreamHandler(constants.DataSyncProtocol)
		ps.SyncVars.Node.RemoveStreamHandler(constants.AvailabilityProtocol)
	}

	if ps.cancel != nil {
		ps.cancel()
		if ps.SyncVars.Node != nil {
			RemoveStreams()
		}
	}
	ps.cancel = cancel
	ps.mu.Unlock()

	// Register Handlers using Sync Package
	if err := syncHandler.HandlePriorSync(ctx, ps.SyncVars.Node); err != nil {
		return err
	}

	if err := syncHandler.HandleMerkle(ctx, ps.SyncVars.Node); err != nil {
		return err
	}

	if err := syncHandler.HandleHeaderSync(ctx, ps.SyncVars.Node); err != nil {
		return err
	}

	if err:= syncHandler.HandleDataSync(ctx, ps.SyncVars.Node); err != nil {
		return err
	}

	if err:= syncHandler.HandleAvailability(ctx, ps.SyncVars.Node); err != nil{
		return err
	}

	// Block until parent or Close() cancels
	<-ctx.Done()

	// Unregister handlers when stopping
	RemoveStreams()

	// Clear stored cancel/node
	ps.mu.Lock()
	ps.cancel = nil
	ps.SyncVars.Node = nil
	ps.mu.Unlock()

	return ctx.Err()
}

// PriorSync builds a PriorSync request from the local node's state (block details,
// merkle tree, checksum), sends it to the remote peer, writes the response to WAL,
// and returns the protobuf response.
func (ps *PriorSync) PriorSync(local_start, local_end, remote_start, remote_end uint64, remote *types.Nodeinfo) (*priorsyncpb.PriorSyncMessage, error) {
	if remote == nil {
		return nil, errors.New("remote is nil")
	}
	if ps.SyncVars.Node == nil {
		return nil, errors.New("host not set — call SetSyncVars with a valid host first")
	}

	ctx := ps.SyncVars.Ctx
	blockInfo := ps.SyncVars.NodeInfo.BlockInfo

	// 1. Get the latest block details from local DB
	localDetails := blockInfo.GetBlockDetails()

	// 2. Build the local Merkle tree and convert to proto snapshot
	merkleDb := merkle.NewMerkleProof(blockInfo)
	builder, err := merkleDb.GenerateMerkleTree(ctx, local_start, local_end)
	if err != nil {
		return nil, fmt.Errorf("failed to generate merkle tree: %w", err)
	}
	pbSnapshot, err := merkleDb.ToSnapshot(ctx, builder)
	if err != nil {
		return nil, fmt.Errorf("failed to convert merkle snapshot: %w", err)
	}

	// 3. Build the PriorSync request message
	reqMsg := types.PriorSyncMessage{
		Priorsync: &types.PriorSync{
			Blocknumber: localDetails.Blocknumber,
			Stateroot:   localDetails.Stateroot,
			Blockhash:   localDetails.Blockhash,
			Range: &types.Range{
				Start: remote_start,
				End:   remote_end,
			},
			Metadata: types.Metadata{},
		},
		Phase: &types.Phase{
			PresentPhase:    constants.SYNC_REQUEST,
			SuccessivePhase: constants.SYNC_REQUEST_RESPONSE,
			Success:         true,
		},
	}

	// 4. Compute checksum
	checksumBytes, err := checksum_priorsync.PriorSyncChecksum().Create(*reqMsg.Priorsync, ps.SyncVars.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to create checksum: %w", err)
	}
	reqMsg.Priorsync.Metadata.Checksum = checksumBytes
	reqMsg.Priorsync.Metadata.Version = ps.SyncVars.Version

	// 5. Send the PriorSync request to the remote peer
	comm := communication.NewCommunication(ps.SyncVars.Node, ps.SyncVars.Version)
	resp, err := comm.SendPriorSync(ctx, pbSnapshot, *remote, reqMsg)
	if err != nil {
		return nil, fmt.Errorf("PriorSync request failed: %w", err)
	}

	logging.Logger(logging.PriorSync).Info(ctx, "PriorSync response received",
		ion.String("remote", remote.PeerID.String()))

	// 6. Convert to proto and write to WAL (if configured)
	protoResp := priorsync_converter.TypesToProtoMessage(resp)
	if ps.SyncVars.WAL != nil {
		event := &WAL.PriorSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Message:   protoResp,
		}
		if _, err := ps.SyncVars.WAL.WriteEvent(event); err != nil {
			return nil, fmt.Errorf("WAL write failed: %w", err)
		}
		if err := ps.SyncVars.WAL.Flush(); err != nil {
			return nil, fmt.Errorf("WAL flush failed: %w", err)
		}
	}

	return protoResp, nil
}

func (ps *PriorSync) Close() {
	ps.mu.Lock()
	cancel := ps.cancel
	node := ps.SyncVars.Node
	ps.mu.Unlock()

	if cancel != nil {
		cancel() // stops the HandlePriorSync wait + makes handler exit quickly
	}
	if node != nil {
		node.RemoveStreamHandler(constants.PriorSyncProtocol)
		node.RemoveStreamHandler(constants.MerkleProtocol) // Also remove Merkle handler
		node.RemoveStreamHandler(constants.HeaderSyncProtocol)
	}
}
