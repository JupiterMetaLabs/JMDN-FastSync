package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

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

		// If shutting down, refuse work quickly
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Prevent stuck reads
		_ = s.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer s.SetReadDeadline(time.Time{})

		var req types.PriorSync
		if err := json.NewDecoder(s).Decode(&req); err != nil {
			return
		}

		// TODO: validate + respond
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

	// TODO: implement:
	// - resolve peer.ID from peer Nodeinfo (whatever field holds libp2p peer.ID)
	// - open stream: node.NewStream(ctx, peerID, ps.SyncVars.ProtocolID)
	// - encode+write data
	// - read response/ack (optional)
	// - close stream

	_ = peer
	_ = data

	return errors.New("not implemented")
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
