package priorsync

import (
	"context"
	"errors"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	sync_proto "github.com/JupiterMetaLabs/JMDN-FastSync/core/sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
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

	// Initialize Communication
	comm := communication.NewCommunication(node, ps.SyncVars.Version)

	// Initialize Sync Handler (Builder Pattern)
	syncHandler := sync_proto.NewSyncHandler(&ps.SyncVars.NodeInfo, comm)

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
