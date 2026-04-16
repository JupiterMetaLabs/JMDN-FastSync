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
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
	accountshelper "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper/accounts"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
	artpkg "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
	"google.golang.org/protobuf/types/known/structpb"
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
		var totalKeys uint64

		// writeMsg serialises onto the AccountSync control stream.
		// All writes (batch_ack, heartbeat, end) share this mutex because
		// libp2p streams are not goroutine-safe.
		writeMsg := func(msg *accountspb.AccountSyncServerMessage) error {
			mu.Lock()
			defer mu.Unlock()
			_ = str.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
			return pbstream.WriteDelimited(str, msg)
		}

		// ── 1. Upload loop ──────────────────────────────────────────────
		// Client sends ART chunks sequentially.  Each chunk is merged into
		// the server's SwappableART; the server acks each one before the
		// client sends the next.  When is_last=true the upload is complete.
		//
		// The final batch is handled inline rather than via HandleAccountsSync
		// to avoid triggering the premature diff computation in ACCOUNTS_SYNC
		// case true (which also does not merge the last chunk).
		for {
			_ = str.SetReadDeadline(time.Now().Add(constants.StreamDeadline))

			req := &accountspb.AccountNonceSyncRequest{}
			if err := pbstream.ReadDelimited(str, req); err != nil {
				return
			}

			if req.IsLast {
				totalKeys = req.TotalKeys

				ok, authErr := s.Datarouter.Authenticate(ctx, req.GetPhase().GetAuth(), remoteNodeInfo)
				if authErr != nil || !ok {
					_ = writeMsg(&accountspb.AccountSyncServerMessage{
						Payload: &accountspb.AccountSyncServerMessage_BatchAck{
							BatchAck: &accountspb.AccountBatchAck{
								BatchIndex: req.BatchIndex,
								Ack:        &ackpb.Ack{Ok: false, Error: errors.AuthenticationFailed.Error()},
							},
						},
					})
					return
				}
				defer s.Datarouter.ResetTTL(ctx, req.GetPhase().GetAuth(), remoteNodeInfo) //nolint:revive

				// Decode and merge the last ART chunk before running diff.
				if len(req.GetArt()) > 0 {
					chunkART, decErr := artpkg.Decode(req.GetArt())
					if decErr != nil {
						_ = writeMsg(&accountspb.AccountSyncServerMessage{
							Payload: &accountspb.AccountSyncServerMessage_BatchAck{
								BatchAck: &accountspb.AccountBatchAck{
									BatchIndex: req.BatchIndex,
									Ack:        &ackpb.Ack{Ok: false, Error: decErr.Error()},
								},
							},
						})
						return
					}
					if mergeErr := s.Datarouter.Nodeinfo.ART.Merge(chunkART); mergeErr != nil {
						_ = writeMsg(&accountspb.AccountSyncServerMessage{
							Payload: &accountspb.AccountSyncServerMessage_BatchAck{
								BatchAck: &accountspb.AccountBatchAck{
									BatchIndex: req.BatchIndex,
									Ack:        &ackpb.Ack{Ok: false, Error: mergeErr.Error()},
								},
							},
						})
						return
					}
				}

				if err := writeMsg(&accountspb.AccountSyncServerMessage{
					Payload: &accountspb.AccountSyncServerMessage_BatchAck{
						BatchAck: &accountspb.AccountBatchAck{
							BatchIndex: req.BatchIndex,
							Ack:        &ackpb.Ack{Ok: true},
						},
					},
				}); err != nil {
					return
				}
				break // all chunks received — proceed to diff
			}

			// Intermediate batch: delegate auth + merge to Datarouter.
			result := s.Datarouter.HandleAccountsSync(ctx, req, remoteNodeInfo)

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
		}

		s.Debug(ctx, constants.AccountsSyncProtocol, node, remoteNodeInfo)

		// ── 2. Heartbeat goroutine ──────────────────────────────────────
		// Keeps the control stream alive while ComputeAccountDiff scans the
		// full account set on the server side.
		computeCtx, computeCancel := context.WithCancel(ctx)
		defer computeCancel()

		done := make(chan struct{})

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
					hb := &accountspb.AccountSyncServerMessage{
						Payload: &accountspb.AccountSyncServerMessage_Heartbeat{
							Heartbeat: &accountspb.AccountSyncHeartbeat{
								Timestamp: time.Now().UnixNano(),
							},
						},
					}
					if err := writeMsg(hb); err != nil {
						logging.Logger(logging.Sync).Warn(computeCtx,
							"accountssync heartbeat write failed, cancelling computation",
							ion.Err(err))
						computeCancel()
						return
					}
				}
			}
		}()

		// ── 3. Diff computation ─────────────────────────────────────────
		diff, diffErr := accountshelper.ComputeAccountDiff(
			computeCtx,
			s.Datarouter.Nodeinfo.ART,
			s.Datarouter.Nodeinfo.BlockInfo,
			totalKeys,
		)
		close(done)

		if diffErr != nil {
			_ = writeMsg(&accountspb.AccountSyncServerMessage{
				Payload: &accountspb.AccountSyncServerMessage_End{
					End: &accountspb.AccountSyncEndOfStream{
						Ack: &ackpb.Ack{Ok: false, Error: diffErr.Error()},
						Phase: &phasepb.Phase{
							PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
							SuccessivePhase: constants.FAILURE,
							Success:         false,
							Error:           diffErr.Error(),
						},
					},
				},
			})
			return
		}

		// ── 4. Collect + paginate missing accounts ──────────────────────
		accounts := make([]*types.Account, 0, len(diff.Missing))
		for _, acc := range diff.Missing {
			accounts = append(accounts, acc)
		}

		pageSize := constants.MAX_ACCOUNT_NONCES
		totalPages := (len(accounts) + pageSize - 1) / pageSize

		// ── 5. Parallel data streams (AccountsSyncDataProtocol) ─────────
		// Each goroutine opens a new stream to the client, sends one page of
		// AccountSyncResponse, waits for the client ack, then closes.
		// The server dials back using the existing connection — no extra
		// handshake needed since the client already connected to us.
		var wg gosync.WaitGroup
		for i := 0; i < totalPages; i++ {
			start := i * pageSize
			end := min(start+pageSize, len(accounts))
			pageIdx := uint32(i)

			pbAccounts := make([]*accountspb.Account, 0, end-start)
			for _, acc := range accounts[start:end] {
				pbAcc := &accountspb.Account{
					DidAddress:  acc.DIDAddress,
					Address:     acc.Address.Bytes(),
					Balance:     "0",
					Nonce:       acc.Nonce,
					AccountType: acc.AccountType,
					CreatedAt:   acc.CreatedAt,
					UpdatedAt:   acc.UpdatedAt,
				}
				if len(acc.Metadata) > 0 {
					if st, stErr := structpb.NewStruct(acc.Metadata); stErr == nil {
						pbAcc.Metadata = st
					}
				}
				pbAccounts = append(pbAccounts, pbAcc)
			}

			wg.Add(1)
			go func(pbAccs []*accountspb.Account, idx uint32) {
				defer wg.Done()

				dataStream, sErr := node.NewStream(ctx, remoteNodeInfo.PeerID, constants.AccountsSyncDataProtocol)
				if sErr != nil {
					logging.Logger(logging.Sync).Warn(ctx, "accountssync: failed to open data stream",
						ion.Err(sErr), ion.Int("page_index", int(idx)))
					return
				}
				defer dataStream.Close()

				_ = dataStream.SetWriteDeadline(time.Now().Add(constants.StreamDeadline))
				page := &accountspb.AccountSyncServerMessage{
					Payload: &accountspb.AccountSyncServerMessage_Response{
						Response: &accountspb.AccountSyncResponse{
							Accounts:  pbAccs,
							PageIndex: idx,
							Ack:       &ackpb.Ack{Ok: true},
							Phase: &phasepb.Phase{
								PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
								SuccessivePhase: constants.ACCOUNTS_SYNC_RESPONSE,
								Success:         true,
							},
						},
					},
				}
				if wErr := pbstream.WriteDelimited(dataStream, page); wErr != nil {
					logging.Logger(logging.Sync).Warn(ctx, "accountssync: page write failed",
						ion.Err(wErr), ion.Int("page_index", int(idx)))
					return
				}

				// Wait for client ack before marking this page done.
				_ = dataStream.SetReadDeadline(time.Now().Add(constants.StreamDeadline))
				clientAck := &ackpb.Ack{}
				if rErr := pbstream.ReadDelimited(dataStream, clientAck); rErr != nil {
					logging.Logger(logging.Sync).Warn(ctx, "accountssync: client ack read failed",
						ion.Err(rErr), ion.Int("page_index", int(idx)))
				}
			}(pbAccounts, pageIdx)
		}
		wg.Wait()

		// ── 6. EndOfStream on the control stream ───────────────────────
		// Sent sequentially after all page acks are received.
		// Signals the client that the full account set has been transferred.
		_ = writeMsg(&accountspb.AccountSyncServerMessage{
			Payload: &accountspb.AccountSyncServerMessage_End{
				End: &accountspb.AccountSyncEndOfStream{
					TotalPages:    uint32(totalPages),
					TotalAccounts: uint64(len(accounts)),
					Ack:           &ackpb.Ack{Ok: true},
					Phase: &phasepb.Phase{
						PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
						SuccessivePhase: constants.SUCCESS,
						Success:         true,
					},
				},
			},
		})
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