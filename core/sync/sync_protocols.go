package sync

import (
	"context"
	gosync "sync"
	"time"

	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	"github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"

	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headerpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	pubsubpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pubsub"
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/pubsub/publisher"
)

type Sync struct {
	debug      bool
	nodeinfo   *types.Nodeinfo
	Datarouter *router.Datarouter
	pubsub     publisher.Publisher_router
}

type sync_interface interface {
	HandleAvailability(ctx context.Context, node host.Host) error
	HandlePriorSync(ctx context.Context, node host.Host) error
	HandleMerkle(ctx context.Context, node host.Host) error
	HandleHeaderSync(ctx context.Context, node host.Host) error
	HandleDataSync(ctx context.Context, node host.Host) error
	HandlePoTSSync(ctx context.Context, node host.Host) error
	HandlePubsub(ctx context.Context, node host.Host) error
	HandleAccountsSync(ctx context.Context, node host.Host) error
	HandleAccountsSyncData(ctx context.Context, node host.Host) error
	Debug(ctx context.Context, protocol protocol.ID, node host.Host, remote *types.Nodeinfo)
}

func NewSyncHandler(nodeinfo *types.Nodeinfo, comm communication.Communicator, debug bool) sync_interface {
	return &Sync{
		debug:      debug,
		nodeinfo:   nodeinfo,
		Datarouter: router.NewDatarouter(nodeinfo, comm),
	}
}

func (s *Sync) HandleAvailability(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.AvailabilityProtocol, func(str network.Stream) {
		defer str.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &availabilitypb.AvailabilityRequest{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		// Requested remote peer
		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		// Route to Datarouter
		resp := s.Datarouter.HandleAvailability(ctx, req, remoteNodeInfo)
		s.Debug(ctx, constants.AvailabilityProtocol, node, remoteNodeInfo)

		// Send response
		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)
	})
	return nil
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

		// ── 1. Read the incoming request ──────────────────────────────────
		_ = str.SetReadDeadline(time.Now().Add(constants.StreamDeadline))
		defer str.SetReadDeadline(time.Time{})

		req := &priorsyncpb.PriorSyncMessage{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		// Requested remote peer
		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		// ── 2. Start heartbeat goroutine ──────────────────────────────────
		// Sends StreamMessage{Heartbeat} every HeartbeatInterval to keep the
		// requester's read deadline alive while computation proceeds.
		//
		// If a heartbeat write fails (Node 2 disconnected / broken pipe), we
		// cancel the computation context so the handler tears down promptly
		// instead of spinning on a dead stream.
		computeCtx, computeCancel := context.WithCancel(ctx)
		defer computeCancel()

		done := make(chan struct{})
		var mu gosync.Mutex

		go func() {
			ticker := time.NewTicker(constants.HeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-computeCtx.Done():
					return
				case <-ticker.C:
					hb := &priorsyncpb.StreamMessage{
						Payload: &priorsyncpb.StreamMessage_Heartbeat{
							Heartbeat: &priorsyncpb.Heartbeat{
								Timestamp: time.Now().UnixNano(),
							},
						},
					}
					mu.Lock()
					_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
					err := pbstream.WriteDelimited(str, hb)
					mu.Unlock()

					if err != nil {
						// Node 2 is gone — cancel computation to avoid starvation.
						logging.Logger(logging.Sync).Warn(computeCtx, "heartbeat write failed, cancelling computation",
							ion.Err(err))
						computeCancel()
						return
					}
				}
			}
		}()

		// ── 3. Run the (potentially long) computation ────────────────────
		resp := s.Datarouter.HandlePriorSync(computeCtx, req, remoteNodeInfo)
		s.Debug(ctx, constants.PriorSyncProtocol, node, remoteNodeInfo)

		// ── 4. Stop heartbeats and send final response ───────────────────
		close(done)

		final := &priorsyncpb.StreamMessage{
			Payload: &priorsyncpb.StreamMessage_Response{Response: resp},
		}

		mu.Lock()
		_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
		_ = pbstream.WriteDelimited(str, final)
		mu.Unlock()
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
			Version:   s.nodeinfo.Version,
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

		// Always create remoteNodeInfo for authentication
		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		// Route to Datarouter
		resp := s.Datarouter.HandleHeaderSync(ctx, req, remoteNodeInfo)
		s.Debug(ctx, constants.HeaderSyncProtocol, node, remoteNodeInfo)

		// Send response
		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)
	})
	return nil
}

func (s *Sync) HandleDataSync(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.DataSyncProtocol, func(str network.Stream) {
		defer str.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &datasyncpb.DataSyncRequest{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		var remoteNodeInfo *types.Nodeinfo
		remoteNodeInfo = &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		// ── Start heartbeat goroutine ──────────────────────────────────
		computeCtx, computeCancel := context.WithCancel(ctx)
		defer computeCancel()

		done := make(chan struct{})
		var mu gosync.Mutex

		go func() {
			ticker := time.NewTicker(constants.HeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-computeCtx.Done():
					return
				case <-ticker.C:
					hb := &datasyncpb.DataSyncStreamMessage{
						Payload: &datasyncpb.DataSyncStreamMessage_Heartbeat{
							Heartbeat: &datasyncpb.DataSyncHeartbeat{
								Timestamp: time.Now().UnixNano(),
							},
						},
					}
					mu.Lock()
					_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
					err := pbstream.WriteDelimited(str, hb)
					mu.Unlock()

					if err != nil {
						logging.Logger(logging.Sync).Warn(computeCtx, "datasync heartbeat write failed, cancelling computation",
							ion.Err(err))
						computeCancel()
						return
					}
				}
			}
		}()

		// ── Run the (potentially long) computation ────────────────────
		resp := s.Datarouter.HandleDataSync(computeCtx, req, remoteNodeInfo)
		s.Debug(ctx, constants.DataSyncProtocol, node, remoteNodeInfo)

		// ── Stop heartbeats and send final response ───────────────────
		close(done)

		final := &datasyncpb.DataSyncStreamMessage{
			Payload: &datasyncpb.DataSyncStreamMessage_Response{Response: resp},
		}

		mu.Lock()
		_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
		err := pbstream.WriteDelimited(str, final)
		if err != nil {
			logging.Logger(logging.Sync).Warn(ctx, "failed to write final datasync response",
				ion.Err(err))
		}
		mu.Unlock()
	})
	return nil
}

func (s *Sync) HandlePoTSSync(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.PoTSProtocol, func(str network.Stream) {
		defer str.Close()

		// refuse work if shutting down
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &potspb.PoTSRequest{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			return
		}

		var remoteNodeInfo *types.Nodeinfo
		remoteNodeInfo = &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		// ── Start heartbeat goroutine ──────────────────────────────────
		computeCtx, computeCancel := context.WithCancel(ctx)
		defer computeCancel()

		done := make(chan struct{})
		var mu gosync.Mutex

		go func() {
			ticker := time.NewTicker(constants.HeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-computeCtx.Done():
					return
				case <-ticker.C:
					hb := &potspb.PoTSStreamMessage{
						Payload: &potspb.PoTSStreamMessage_Heartbeat{
							Heartbeat: &potspb.PoTSHeartbeat{
								Timestamp: time.Now().UnixNano(),
							},
						},
					}
					mu.Lock()
					_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
					err := pbstream.WriteDelimited(str, hb)
					mu.Unlock()

					if err != nil {
						logging.Logger(logging.Sync).Warn(computeCtx, "PoTS heartbeat write failed, cancelling computation",
							ion.Err(err))
						computeCancel()
						return
					}
				}
			}
		}()

		// ── Run the (potentially long) computation ────────────────────
		resp := s.Datarouter.HandlePoTSync(computeCtx, req, remoteNodeInfo)
		s.Debug(ctx, constants.PoTSProtocol, node, remoteNodeInfo)

		// ── Stop heartbeats and send final response ───────────────────
		close(done)

		final := &potspb.PoTSStreamMessage{
			Payload: &potspb.PoTSStreamMessage_Response{Response: resp},
		}

		mu.Lock()
		_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
		err := pbstream.WriteDelimited(str, final)
		if err != nil {
			logging.Logger(logging.Sync).Warn(ctx, "failed to write final PoTS response",
				ion.Err(err))
		}
		mu.Unlock()
	})
	return nil
}

func (s *Sync) HandlePubsub(ctx context.Context, node host.Host) error {
	if s.pubsub == nil {
		s.pubsub = publisher.NewPublisher().SetPublisher(ctx, node).StartPublisher()
	}
	node.SetStreamHandler(constants.BlocksPUBSUB, func(str network.Stream) {
		// refuse work if shutting down
		select {
		case <-ctx.Done():
			str.Reset()
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer str.SetReadDeadline(time.Time{})

		req := &pubsubpb.SubscribeRequest{}
		if err := pbstream.ReadDelimited(str, req); err != nil {
			str.Reset()
			return
		}

		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		s.Debug(ctx, constants.BlocksPUBSUB, node, remoteNodeInfo)

		// Decide whether to accept the subscription.
		// Each guard returns immediately on failure — no chained conditions.
		accepted, rejectReason := func() (bool, string) {
			if !availability.FastsyncReady().AmIAvailable() {
				return false, errors.FastsyncNotAvailable.Error()
			}
			ok, err := s.Datarouter.Authenticate(ctx, req.GetAuth(), remoteNodeInfo)
			if err != nil {
				return false, err.Error()
			}
			if !ok {
				return false, errors.AuthenticationFailed.Error()
			}
			return true, ""
		}()

		var resp *pubsubpb.SubscribeResponse
		if accepted {
			s.pubsub.AddStream(str)
			resp = &pubsubpb.SubscribeResponse{Accepted: true}
		} else {
			resp = &pubsubpb.SubscribeResponse{Accepted: false, Error: rejectReason}
		}

		_ = str.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, resp)

		if !resp.Accepted {
			str.Close()
		}
	})
	return nil
}

func (s *Sync) HandleAccountsSync(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.AccountsSyncProtocol, func(str network.Stream) {
		defer str.Close()

		select {
		case <-ctx.Done():
			return
		default:
		}

		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		var mu gosync.Mutex

		writeMsg := func(msg *accountspb.AccountSyncServerMessage) error {
			mu.Lock()
			defer mu.Unlock()
			_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
			return pbstream.WriteDelimited(str, msg)
		}

		// Single heartbeat goroutine for the entire stream duration.
		// computeCtx is shared across all iterations:
		//   - defer computeCancel() fires on any return path → heartbeat exits.
		//   - heartbeat write failure calls computeCancel() → in-progress
		//     HandleAccountsSync unblocks → loop exits on next read error.
		computeCtx, computeCancel := context.WithCancel(ctx)
		defer computeCancel()

		go func() {
			ticker := time.NewTicker(constants.HeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-computeCtx.Done():
					return
				case <-ticker.C:
					hb := &accountspb.AccountSyncServerMessage{
						Payload: &accountspb.AccountSyncServerMessage_Heartbeat{
							Heartbeat: &accountspb.AccountSyncHeartbeat{
								Timestamp: time.Now().UnixNano(),
							},
						},
					}
					if err := writeMsg(hb); err != nil {
						logging.Logger(logging.Sync).Warn(computeCtx,
							"accountssync heartbeat write failed, cancelling stream",
							ion.Err(err))
						computeCancel()
						return
					}
				}
			}
		}()

		// Upload loop — reads one ART chunk per iteration, routes to Datarouter.
		// Datarouter handles IsLast: non-last merges only, last merges + diffs.
		// result.Phase.SuccessivePhase drives the response:
		//   ACCOUNTS_SYNC_REQUEST → BatchAck, continue (more chunks expected)
		//   anything else         → EndOfStream, return (diff done or error)
		for {
			_ = str.SetReadDeadline(time.Now().Add(constants.StreamDeadline))

			req := &accountspb.AccountNonceSyncRequest{}
			if err := pbstream.ReadDelimited(str, req); err != nil {
				return
			}

			result := s.Datarouter.HandleAccountsSync(computeCtx, req, remoteNodeInfo)
			s.Debug(ctx, constants.AccountsSyncProtocol, node, remoteNodeInfo)

			if result.Phase.SuccessivePhase == constants.ACCOUNTS_SYNC_REQUEST {
				if err := writeMsg(&accountspb.AccountSyncServerMessage{
					Payload: &accountspb.AccountSyncServerMessage_BatchAck{
						BatchAck: &accountspb.AccountBatchAck{
							BatchIndex: req.BatchIndex,
							Ack:        result.Ack,
						},
					},
				}); err != nil {
					return
				}
				if !result.Ack.Ok {
					return
				}
				continue
			}

			// Diff complete (or error) — EndOfStream is the final message.
			// return triggers defer str.Close() for this handler goroutine only;
			// the server remains up and accepts other client connections.
			_ = writeMsg(&accountspb.AccountSyncServerMessage{
				Payload: &accountspb.AccountSyncServerMessage_End{
					End: result,
				},
			})
			return
		}
	})
	return nil
}

// HandleAccountsSyncData registers the client-side handler for the
// AccountsSyncDataProtocol stream.  The server dials this protocol for each
// page of missing accounts it wants to deliver; the client reads the page,
// routes it to the Datarouter for storage, and acks before the stream closes.
//
// Pattern mirrors HandleMerkle: single read → route → single write.
func (s *Sync) HandleAccountsSyncData(ctx context.Context, node host.Host) error {
	node.SetStreamHandler(constants.AccountsSyncDataProtocol, func(str network.Stream) {
		defer str.Close()

		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = str.SetReadDeadline(time.Now().Add(constants.StreamDeadline))
		defer str.SetReadDeadline(time.Time{})

		page := &accountspb.AccountSyncServerMessage{}
		if err := pbstream.ReadDelimited(str, page); err != nil {
			return
		}

		remoteNodeInfo := &types.Nodeinfo{
			PeerID:    str.Conn().RemotePeer(),
			Multiaddr: []multiaddr.Multiaddr{str.Conn().RemoteMultiaddr()},
			Version:   s.nodeinfo.Version,
		}

		s.Debug(ctx, constants.AccountsSyncDataProtocol, node, remoteNodeInfo)

		_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
		defer str.SetWriteDeadline(time.Time{})

		_ = pbstream.WriteDelimited(str, &ackpb.Ack{Ok: true})
	})
	return nil
}

func (s *Sync) Debug(ctx context.Context, protocol protocol.ID, node host.Host, remote *types.Nodeinfo) {
	if s.debug && remote != nil {
		logging.Logger(logging.Sync).Debug(ctx, "Sync Protocols Debug",
			ion.String("protocol", string(protocol)),
			ion.String("peerID", remote.PeerID.String()),
			ion.String("multiaddr", remote.Multiaddr[0].String()))
	}
}