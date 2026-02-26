package sync

import (
	"context"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"

	headerpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
)

type Sync struct {
	debug bool
	nodeinfo   *types.Nodeinfo
	Datarouter *router.Datarouter
}

type sync_interface interface {
	HandlePriorSync(ctx context.Context, node host.Host) error
	HandleMerkle(ctx context.Context, node host.Host) error
	HandleHeaderSync(ctx context.Context, node host.Host) error
	Debug(ctx context.Context, protocol protocol.ID, node host.Host, remote *types.Nodeinfo)
}

func NewSyncHandler(nodeinfo *types.Nodeinfo, comm communication.Communicator, debug bool) sync_interface {
	return &Sync{
		debug:      debug,
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

		// Requested remote peer
		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
		}

		// Route to Datarouter
		resp := s.Datarouter.HandlePriorSync(ctx, req, remoteNodeInfo)
		s.Debug(ctx, constants.PriorSyncProtocol, node, remoteNodeInfo)

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

		// Requested remote peer
		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
		}

		// Route to Datarouter
		resp := s.Datarouter.HandleMerkle(ctx, req, remoteNodeInfo)
		s.Debug(ctx, constants.MerkleProtocol, node, remoteNodeInfo)

		// Send response
		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)
	})
	return nil
}

func (s *Sync) HandleHeaderSync(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.HeaderSyncProtocol, func(str network.Stream) {
		defer str.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &headerpb.HeaderSyncRequest{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		var remoteNodeInfo *types.Nodeinfo
		if s.debug {
			remoteNodeInfo = &types.Nodeinfo{
				PeerID:    str.Conn().RemotePeer(),
				Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			}
		}

		// Route to Datarouter
		resp := s.Datarouter.HandleHeaderSync(ctx, req)
		s.Debug(ctx, constants.HeaderSyncProtocol, node, remoteNodeInfo)

		// Send response
		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)
	})
	return nil
}

func (s *Sync) Debug(ctx context.Context, protocol protocol.ID, node host.Host, remote *types.Nodeinfo) {
	if s.debug {
		logging.Logger(logging.Sync).Info(ctx, "Sync Protocols Debug", 
		ion.String("protocol",  string(protocol)), 
		ion.String("peerID", remote.PeerID.String()), 
		ion.String("multiaddr", remote.Multiaddr[0].String()))
	}
}
