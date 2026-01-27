package protocol

import (
	"context"
	"errors"
	"sync"
	"time"

	coreprotocol "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	libp2p_peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

type PriorSync struct {
	PriorSync *types.PriorSync
	SyncVars  *types.Syncvars

	mu     sync.Mutex
	cancel context.CancelFunc
	node   host.Host
}

func NewPriorSyncRouter() Priorsync_router {
	return &PriorSync{
		PriorSync: &types.PriorSync{},
		SyncVars:  &types.Syncvars{},
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

	datarouter := router.NewDatarouter()

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

		req := &priorsyncpb.PriorSync{}
		if err := pbstream.ReadDelimited(s, req); err != nil {
			return
		}

		// Route to Datarouter for state-based processing
		resp := datarouter.HandlePriorSync(req)

		// Send acknowledgment
		_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer s.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(s, resp)
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

// SendPriorSync sends a PriorSync message to a peer.
// Usually you’ll do node.NewStream(ctx, peerID, protoID) and write the payload.
func (ps *PriorSync) SendPriorSync(
	peer types.Nodeinfo,
	data types.PriorSync,
) error {
	if ps.node == nil {
		return errors.New("host is nil")
	}
	if ps.SyncVars == nil || ps.SyncVars.Ctx == nil {
		return errors.New("sync vars ctx not set")
	}

	// Convert types.PriorSync to protobuf PriorSync
	req := &priorsyncpb.PriorSync{
		Blocknumber: data.Blocknumber,
		Stateroot:   data.Stateroot,
		Blockhash:   data.Blockhash,
		Metadata: &priorsyncpb.Metadata{
			Checksum: data.Metadata.Checksum,
			State:    data.Metadata.State,
			Version:  uint32(data.Metadata.Version),
		},
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peer.PeerID,
		Addrs: peer.Multiaddr,
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using SendProto
	if err := coreprotocol.SendProto(ps.SyncVars.Ctx, ps.node, peerInfo, ps.SyncVars.Protocol, req, resp); err != nil {
		return errors.New("failed to send priorsync: " + err.Error())
	}

	// Check acknowledgment
	if resp.Ack == nil {
		return errors.New("no acknowledgment received")
	}
	if !resp.Ack.Ok {
		return errors.New("sync failed: " + resp.Ack.Error)
	}

	return nil
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
