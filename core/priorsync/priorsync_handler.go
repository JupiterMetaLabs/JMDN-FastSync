package protocol

import (
	"context"
	"errors"
	"sync"

	sync_proto "github.com/JupiterMetaLabs/JMDN-FastSync/core/sync"
	libp2p_peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"github.com/libp2p/go-libp2p/core/host"
)

type PriorSync struct {
	PriorSync *types.PriorSyncMessage
	SyncVars  *types.Syncvars

	mu     sync.Mutex
	cancel context.CancelFunc
	node   host.Host
}

func NewPriorSyncRouter() Priorsync_router {
	return &PriorSync{
		PriorSync: &types.PriorSyncMessage{},
		SyncVars:  &types.Syncvars{},
		mu:        sync.Mutex{},
	}
}

func (ps *PriorSync) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo) Priorsync_router {
	if ps.SyncVars == nil {
		ps.SyncVars = &types.Syncvars{}
	}
	ps.SyncVars.Version = protocolVersion
	ps.SyncVars.NodeInfo = nodeInfo
	ps.SyncVars.Ctx = ctx
	return ps
}

func (ps *PriorSync) HandlePriorSync(node host.Host) error {
	if node == nil {
		return errors.New("host is nil")
	}
	if ps.SyncVars == nil || ps.SyncVars.Ctx == nil {
		return errors.New("sync vars ctx not set")
	}

	// derive child from parent; child cannot outlive parent
	ctx, cancel := context.WithCancel(ps.SyncVars.Ctx)

	// Initialize Sync Handler (Builder Pattern)
	syncHandler := sync_proto.NewSyncHandler(&ps.SyncVars.NodeInfo)

	ps.mu.Lock()

	// If called twice, stop the old one first

	var RemoveStreams func()
	RemoveStreams = func() {
		ps.node.RemoveStreamHandler(constants.PriorSyncProtocol)
		ps.node.RemoveStreamHandler(constants.MerkleProtocol)
	}

	if ps.cancel != nil {
		ps.cancel()
		if ps.node != nil {
			RemoveStreams()
		}
	}
	ps.cancel = cancel
	ps.node = node
	ps.mu.Unlock()

	// Register Handlers using Sync Package
	if err := syncHandler.HandlePriorSync(ctx, node); err != nil {
		return err
	}

	if err := syncHandler.HandleMerkle(ctx, node); err != nil {
		return err
	}

	// Block until parent or Close() cancels
	<-ctx.Done()

	// Unregister handlers when stopping
	RemoveStreams()

	// Clear stored cancel/node
	ps.mu.Lock()
	ps.cancel = nil
	ps.node = nil
	ps.mu.Unlock()

	return ctx.Err()
}

// SendPriorSync sends a PriorSync message to a peer and returns the response.
// Usually you'll do node.NewStream(ctx, peerID, protoID) and write the payload.
func (ps *PriorSync) SendPriorSync(
	ctx context.Context,
	// As per the observation, data is synced irregularly so we need to check the missing blocks too so merkle check
	merkle_snapshot *merklepb.MerkleSnapshot,
	// this peer is the one we are sending the prior sync to
	peerNode types.Nodeinfo,
	data types.PriorSyncMessage,
) (*types.PriorSyncMessage, error) {
	if ps.node == nil {
		return nil, errors.New("host is nil")
	}
	if ps.SyncVars == nil || ps.SyncVars.Ctx == nil {
		return nil, errors.New("sync vars ctx not set")
	}
	if merkle_snapshot == nil {
		return nil, errors.New("merkle is nil")
	}

	req := &priorsyncpb.PriorSyncMessage{
		Priorsync: &priorsyncpb.PriorSync{
			Blocknumber:    data.Priorsync.Blocknumber,
			Stateroot:      data.Priorsync.Stateroot,
			Blockhash:      data.Priorsync.Blockhash,
			Merklesnapshot: merkle_snapshot,
			Metadata: &priorsyncpb.Metadata{
				Checksum: data.Priorsync.Metadata.Checksum,
				Version:  uint32(data.Priorsync.Metadata.Version),
			},
		},
		Phase: &phasepb.Phase{
			PresentPhase:    data.Phase.PresentPhase,
			SuccessivePhase: data.Phase.SuccessivePhase,
			Success:         data.Phase.Success,
			Error:           data.Phase.Error,
		},
	}
	if data.Priorsync.Range != nil {
		req.Priorsync.Range = &merklepb.Range{
			Start: data.Priorsync.Range.Start,
			End:   data.Priorsync.Range.End,
		}
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using SendProtoDelimited with full peer info for transport selection
	if err := messaging.SendProtoDelimited(
		ctx,
		ps.SyncVars.Version,
		ps.node,
		peerInfo, // Pass full AddrInfo for transport selection
		constants.PriorSyncProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send priorsync: " + err.Error())
	}

	// Check acknowledgment
	if resp.Ack == nil {
		return nil, errors.New("no acknowledgment received")
	}
	if !resp.Ack.Ok {
		return nil, errors.New("sync failed: " + resp.Ack.Error)
	}

	// Convert protobuf response to types.PriorSyncMessage
	result := &types.PriorSyncMessage{
		Ack: &types.PriorSyncAck{
			Ok:    resp.Ack.Ok,
			Error: resp.Ack.Error,
		},
	}

	// Convert PriorSync if present
	if resp.Priorsync != nil {
		result.Priorsync = &types.PriorSync{
			Blocknumber: resp.Priorsync.Blocknumber,
			Stateroot:   resp.Priorsync.Stateroot,
			Blockhash:   resp.Priorsync.Blockhash,
			Range: &types.Range{
				Start: resp.Priorsync.Range.Start,
				End:   resp.Priorsync.Range.End,
			},
			Metadata: types.Metadata{
				Checksum: resp.Priorsync.Metadata.Checksum,
				Version:  uint16(resp.Priorsync.Metadata.Version),
			},
		}
	}

	return result, nil
}

// SendMerkleRequest sends a MerkleRequestMessage to a peer and returns the response.
func (ps *PriorSync) SendMerkleRequest(
	ctx context.Context,
	peerNode types.Nodeinfo,
	req *merklepb.MerkleRequestMessage,
) (*merklepb.MerkleMessage, error) {
	if ps.node == nil {
		return nil, errors.New("host is nil")
	}
	if ps.SyncVars == nil {
		return nil, errors.New("sync vars not set")
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &merklepb.MerkleMessage{}

	// Send using SendProtoDelimited with MerkleProtocol
	if err := messaging.SendProtoDelimited(
		ctx,
		ps.SyncVars.Version,
		ps.node,
		peerInfo,
		constants.MerkleProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send merkle request: " + err.Error())
	}

	return resp, nil
}

func (ps *PriorSync) Close() {
	ps.mu.Lock()
	cancel := ps.cancel
	node := ps.node
	ps.mu.Unlock()

	if cancel != nil {
		cancel() // stops the HandlePriorSync wait + makes handler exit quickly
	}
	if node != nil {
		node.RemoveStreamHandler(constants.PriorSyncProtocol)
		node.RemoveStreamHandler(constants.MerkleProtocol) // Also remove Merkle handler
	}
}
