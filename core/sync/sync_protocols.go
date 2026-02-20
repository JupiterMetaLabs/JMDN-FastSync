package sync

import (
	"context"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

type Sync struct {
	nodeinfo   *types.Nodeinfo
	Datarouter *router.Datarouter
}

type sync_interface interface {
	HandlePriorSync(ctx context.Context, node host.Host) error
	HandleMerkle(ctx context.Context, node host.Host) error
}

func NewSyncHandler(nodeinfo *types.Nodeinfo, comm communication.CommunicationInterface) sync_interface {
	return &Sync{
		nodeinfo:   nodeinfo,
		Datarouter: router.NewDatarouter(nodeinfo, comm),
	}
}

func (s *Sync) HandlePriorSync(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.PriorSyncProtocol, func(str network.Stream) {
		defer str.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &priorsyncpb.PriorSyncMessage{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		// Route to Datarouter
		resp := s.Datarouter.HandlePriorSync(ctx, req)

		// Send response
		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)
	})
	return nil
}

func (s *Sync) HandleMerkle(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.MerkleProtocol, func(str network.Stream) {
		defer str.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &merklepb.MerkleRequestMessage{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		// Route to Datarouter
		resp := s.Datarouter.HandleMerkle(ctx, req)

		// Send response
		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)
	})
	return nil
}
