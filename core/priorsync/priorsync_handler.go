package protocol

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	libp2p_peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/helper/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
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

func (ps *PriorSync) SetSyncVars(ctx context.Context, protoID protocol.ID, protocolVersion uint16, nodeInfo types.Nodeinfo) Priorsync_router {
	if ps.SyncVars == nil {
		ps.SyncVars = &types.Syncvars{}
	}
	ps.SyncVars.Version = protocolVersion
	ps.SyncVars.Protocol = protoID
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

	datarouter := router.NewDatarouter(&ps.SyncVars.NodeInfo)

	ps.mu.Lock()

	// If called twice, stop the old one first
	if ps.cancel != nil {
		ps.cancel()
		if ps.node != nil {
			ps.node.RemoveStreamHandler(ps.SyncVars.Protocol)
		}
	}
	ps.cancel = cancel
	ps.node = node
	ps.mu.Unlock()

	protoID := ps.SyncVars.Protocol

	node.SetStreamHandler(protoID, func(s network.Stream) {
		defer s.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = s.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer s.SetReadDeadline(time.Time{})

		streamReq := &priorsyncpb.StreamMessage{}
		if err := pbstream.ReadDelimited(s, streamReq); err != nil {
			return
		}

		// Route to Datarouter for state-based processing
		streamResp := datarouter.HandleStreamMessage(ctx, streamReq)

		// Send acknowledgment
		_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer s.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(s, streamResp)
	})

	// Block until parent or Close() cancels
	<-ctx.Done()

	// Unregister handler when stopping
	node.RemoveStreamHandler(protoID)

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
	// As per the observation, data is synced irregularly so we need to check the missing blocks too so merkle check
	merkle_snapshot *merkletree.MerkleTreeSnapshot,
	// this peer is the one we are sending the prior sync to
	peer types.Nodeinfo,
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
			Merklesnapshot: merkle.MerkleSnapshotToProto(merkle_snapshot),
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
		ID:    peer.PeerID,
		Addrs: peer.Multiaddr,
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using SendProtoDelimited with full peer info for transport selection
	if err := messaging.SendProtoDelimited(
		ps.SyncVars.Ctx,
		ps.SyncVars.Version,
		ps.node,
		peerInfo, // Pass full AddrInfo for transport selection
		ps.SyncVars.Protocol,
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

func (ps *PriorSync) Close() {
	ps.mu.Lock()
	cancel := ps.cancel
	node := ps.node
	protoID := protocol.ID("")
	if ps.SyncVars != nil {
		protoID = ps.SyncVars.Protocol
	}
	ps.mu.Unlock()

	if cancel != nil {
		cancel() // stops the HandlePriorSync wait + makes handler exit quickly
	}
	if node != nil && protoID != "" {
		node.RemoveStreamHandler(protoID) // stops new streams immediately
	}
}
