package catchup

import (
	"context"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/accountsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/datasync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/headersync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/pots"
	potshelper "github.com/JupiterMetaLabs/JMDN-FastSync/core/pots/helper"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/reconsillation"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/reconsillation/LRUCache"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const namedlogger = "log:catchup"

// CatchUp holds state for a single catch-up sync session.
type CatchUp struct {
	SyncVars *types.Syncvars
}

// NewCatchUp returns a CatchUp ready for SetSyncVars.
func NewCatchUp() CatchUp_router {
	return &CatchUp{}
}

func (c *CatchUp) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) CatchUp_router {
	if c.SyncVars == nil {
		c.SyncVars = &types.Syncvars{}
	}
	c.SyncVars.Version = protocolVersion
	c.SyncVars.NodeInfo = nodeInfo
	c.SyncVars.Ctx = ctx
	c.SyncVars.WAL = wal
	c.SyncVars.Node = node
	return c
}

func (c *CatchUp) GetSyncVars() *types.Syncvars {
	return c.SyncVars
}

func (c *CatchUp) Close() {
	c.SyncVars = nil
}

// Run executes the full catch-up pipeline:
//
//	Phase 1 — Availability probe
//	Phase 2 — HeaderSync (no Merkle confirmation round-trip)
//	Phase 3 — DataSync
//	Phase 4 — AccountSync
//	Phase 5 — Reconciliation
//	Phase 6 — PoTS gap fill (blocks produced while phases 2-5 ran)
func (c *CatchUp) Run(ctx context.Context, fromBlock uint64, peers []types.Nodeinfo) error {
	if c.SyncVars == nil {
		return fmt.Errorf("catchup: SetSyncVars not called")
	}
	if len(peers) == 0 {
		return fmt.Errorf("catchup: no peers provided")
	}

	// ── Phase 1: Availability probe ───────────────────────────────────────
	// Ask each remote what range it has and get an auth token.
	// fromBlock is the first block we need; pass it as range start so the
	// server can confirm it has data that far.
	Log.Logger(namedlogger).Info(ctx, "catchup: phase 1 — availability probe",
		ion.Uint64("from_block", fromBlock))

	avail := availability.NewAvailability().SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node, c.SyncVars.WAL)
	remotes, err := avail.SendMultipleAvailabilityRequest(ctx, c.SyncVars, peers, fromBlock, 0)
	if err != nil {
		return fmt.Errorf("catchup: availability probe: %w", err)
	}

	// Filter to peers that are available and have data at fromBlock.
	remotes = filterAvailable(remotes, fromBlock)
	if len(remotes) == 0 {
		return fmt.Errorf("catchup: no available peers have data from block %d", fromBlock)
	}

	// remoteTip is the highest block number across all available peers.
	remoteTip := highestBlock(remotes)
	if remoteTip < fromBlock {
		return fmt.Errorf("catchup: remoteTip %d < fromBlock %d — nothing to sync", remoteTip, fromBlock)
	}

	Log.Logger(namedlogger).Info(ctx, "catchup: availability ok",
		ion.Int("peers", len(remotes)),
		ion.Uint64("from_block", fromBlock),
		ion.Uint64("remote_tip", remoteTip))

	// ── PoTS WAL: open before phases 2-5 so blocks produced during the
	// catch-up window are captured and replayed afterwards.
	potsRouter := pots.NewPoTS().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node).
		SetWAL(ctx, c.SyncVars.WAL)
	defer potsRouter.Close()

	// ── Build the catch-up tag: one range [fromBlock..remoteTip] ──────────
	// We already know exactly what's missing — no Merkle bisection needed.
	catchUpTag := &taggingpb.Tag{
		Range: []*taggingpb.RangeTag{
			{Start: fromBlock, End: remoteTip},
		},
	}

	// auth from primary remote (index 0) drives phase and auth fields.
	primaryAuth := remotes[0].GetAuth()

	// ── Phase 2: HeaderSync ───────────────────────────────────────────────
	Log.Logger(namedlogger).Info(ctx, "catchup: phase 2 — header sync",
		ion.Uint64("from", fromBlock),
		ion.Uint64("to", remoteTip))

	hs := headersync.NewHeaderSync().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node, c.SyncVars.WAL)

	headerReq := &headersyncpb.HeaderSyncRequest{
		Tag: catchUpTag,
		Ack: &ackpb.Ack{Ok: true},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.HEADER_SYNC_REQUEST,
			SuccessivePhase: constants.HEADER_SYNC_RESPONSE,
			Success:         true,
			Auth:            primaryAuth,
		},
	}

	// syncConfirmation=false: skip Merkle round-trip, we know the exact range.
	dataSyncReq, err := hs.HeaderSync(headerReq, remotes, false)
	if err != nil {
		return fmt.Errorf("catchup: header sync: %w", err)
	}

	Log.Logger(namedlogger).Info(ctx, "catchup: phase 2 complete")

	// ── Phase 3: DataSync ─────────────────────────────────────────────────
	Log.Logger(namedlogger).Info(ctx, "catchup: phase 3 — data sync")

	ds := datasync.NewDataSync().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node, c.SyncVars.WAL)

	// If HeaderSync returned nil (range was already present), build the
	// DataSyncRequest directly from the catch-up tag.
	if dataSyncReq == nil {
		dataSyncReq = &datasyncpb.DataSyncRequest{
			Tag:     catchUpTag,
			Version: uint32(c.SyncVars.Version),
			Ack:     &ackpb.Ack{Ok: true},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.DATA_SYNC_REQUEST,
				SuccessivePhase: constants.DATA_SYNC_RESPONSE,
				Success:         true,
				Auth:            primaryAuth,
			},
		}
	}

	taggedAccounts, err := ds.DataSync(dataSyncReq, remotes)
	if err != nil {
		return fmt.Errorf("catchup: data sync: %w", err)
	}

	Log.Logger(namedlogger).Info(ctx, "catchup: phase 3 complete")

	// ── Phase 4: AccountSync ──────────────────────────────────────────────
	// Syncs zero-tx accounts not covered by DataSync TaggedAccounts.
	Log.Logger(namedlogger).Info(ctx, "catchup: phase 4 — account sync")

	as := accountsync.NewAccountSync().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node, c.SyncVars.WAL)

	totalSynced, err := as.AccountSync(remotes[0])
	if err != nil {
		// Non-fatal: reconciliation still runs over whatever accounts we have.
		Log.Logger(namedlogger).Warn(ctx, "catchup: account sync failed — continuing",
			ion.Err(err))
	} else {
		Log.Logger(namedlogger).Info(ctx, "catchup: phase 4 complete",
			ion.Uint64("accounts_synced", totalSynced))
	}

	// ── Phase 5: Reconciliation ───────────────────────────────────────────
	Log.Logger(namedlogger).Info(ctx, "catchup: phase 5 — reconciliation")

	lru := LRUCache.NewLRUCache(constants.LRU_CACHE_CAPACITY)
	rec := reconsillation.NewReconciliation().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.WAL).
		SetLRUCache(lru)

	committed, failed, err := rec.Reconcile(taggedAccounts, remotes[0])
	if err != nil {
		return fmt.Errorf("catchup: reconciliation: %w", err)
	}
	if len(failed) > 0 {
		Log.Logger(namedlogger).Warn(ctx, "catchup: some accounts failed reconciliation",
			ion.Int("failed", len(failed)),
			ion.Int("committed", committed))
	} else {
		Log.Logger(namedlogger).Info(ctx, "catchup: phase 5 complete",
			ion.Int("committed", committed))
	}

	// ── Phase 6: PoTS gap fill ────────────────────────────────────────────
	// Fetch blocks that were produced on the remote while phases 2-5 ran.
	Log.Logger(namedlogger).Info(ctx, "catchup: phase 6 — PoTS gap fill")

	if err := c.runPoTS(ctx, potsRouter, remotes, remoteTip); err != nil {
		return fmt.Errorf("catchup: PoTS gap fill: %w", err)
	}

	Log.Logger(namedlogger).Info(ctx, "catchup: all phases complete — node is caught up",
		ion.Uint64("synced_to", remoteTip))

	return nil
}

// runPoTS builds a PoTSRequest from the PoTS WAL and fetches any gap blocks
// that arrived on the remote while phases 2-5 were running.
func (c *CatchUp) runPoTS(ctx context.Context, potsRouter pots.PoTS_router, remotes []*availabilitypb.AvailabilityResponse, syncedTip uint64) error {
	potsWAL, err := potsRouter.GetWAL()
	if err != nil {
		// No WAL means no blocks were buffered — nothing to do.
		Log.Logger(namedlogger).Info(ctx, "catchup: PoTS WAL not initialised — skipping gap fill")
		return nil
	}

	// Read buffered blocks from the PoTS WAL.
	walBlocks, err := potsWAL.Read(ctx, 0, 0) // offset=0, limit=0 → all
	if err != nil {
		return fmt.Errorf("read PoTS WAL: %w", err)
	}

	if len(walBlocks) == 0 {
		Log.Logger(namedlogger).Info(ctx, "catchup: PoTS WAL is empty — no gap blocks")
		return nil
	}

	// Build the blocks map: blockNumber → blockHash.
	blocks := make(map[uint64][]byte, len(walBlocks))
	for _, b := range walBlocks {
		if b != nil {
			blocks[b.BlockNumber] = b.BlockHash
		}
	}

	latestWALBlock, err := potsWAL.GetLatestBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("PoTS WAL latest block: %w", err)
	}

	remoteNodeInfo, err := remoteToNodeinfo(remotes[0])
	if err != nil {
		return fmt.Errorf("PoTS remote nodeinfo: %w", err)
	}

	potsReq := potshelper.NewPoTSRequestBuilder().
		AddMap(blocks).
		AddLatestBlock(latestWALBlock).
		AddAuth(remotes[0].GetAuth()).
		Build()

	potsResp, err := potsRouter.SendPoTSRequest(ctx, potsReq, *remoteNodeInfo)
	if err != nil {
		return fmt.Errorf("PoTS request: %w", err)
	}

	if potsResp.Tag == nil || (len(potsResp.Tag.Range) == 0 && len(potsResp.Tag.BlockNumber) == 0) {
		Log.Logger(namedlogger).Info(ctx, "catchup: PoTS — no gap blocks")
		return nil
	}

	Log.Logger(namedlogger).Info(ctx, "catchup: PoTS gap detected — fetching",
		ion.Int("ranges", len(potsResp.Tag.Range)),
		ion.Int("blocks", len(potsResp.Tag.BlockNumber)))

	// Fetch gap headers and data using existing HeaderSync/DataSync (no confirmation).
	hs := headersync.NewHeaderSync().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node, c.SyncVars.WAL)

	gapHeaderReq := &headersyncpb.HeaderSyncRequest{
		Tag: potsResp.Tag,
		Ack: &ackpb.Ack{Ok: true},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.HEADER_SYNC_REQUEST,
			SuccessivePhase: constants.HEADER_SYNC_RESPONSE,
			Success:         true,
			Auth:            remotes[0].GetAuth(),
		},
	}

	gapDataReq, err := hs.HeaderSync(gapHeaderReq, remotes, false)
	if err != nil {
		return fmt.Errorf("PoTS header sync: %w", err)
	}

	if gapDataReq == nil {
		gapDataReq = &datasyncpb.DataSyncRequest{
			Tag:     potsResp.Tag,
			Version: uint32(c.SyncVars.Version),
			Ack:     &ackpb.Ack{Ok: true},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.DATA_SYNC_REQUEST,
				SuccessivePhase: constants.DATA_SYNC_RESPONSE,
				Success:         true,
				Auth:            remotes[0].GetAuth(),
			},
		}
	}

	ds := datasync.NewDataSync().
		SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.Node, c.SyncVars.WAL)

	gapTaggedAccounts, err := ds.DataSync(gapDataReq, remotes)
	if err != nil {
		return fmt.Errorf("PoTS data sync: %w", err)
	}

	// Reconcile gap accounts.
	if gapTaggedAccounts != nil && len(gapTaggedAccounts.Accounts) > 0 {
		lru := LRUCache.NewLRUCache(constants.LRU_CACHE_CAPACITY)
		rec := reconsillation.NewReconciliation().
			SetSyncVars(ctx, c.SyncVars.Version, c.SyncVars.NodeInfo, c.SyncVars.WAL).
			SetLRUCache(lru)

		committed, failed, err := rec.Reconcile(gapTaggedAccounts, remotes[0])
		if err != nil {
			return fmt.Errorf("PoTS reconciliation: %w", err)
		}
		Log.Logger(namedlogger).Info(ctx, "catchup: PoTS reconciliation complete",
			ion.Int("committed", committed),
			ion.Int("failed", len(failed)))
	}

	// Hydrate PoTS WAL into the main DB.
	if err := potsWAL.Close(); err != nil {
		Log.Logger(namedlogger).Warn(ctx, "catchup: PoTS WAL close failed", ion.Err(err))
	}

	Log.Logger(namedlogger).Info(ctx, "catchup: phase 6 complete")
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// filterAvailable removes peers that are not available, have no auth token, or
// whose reported tip (BlockMerge) does not reach fromBlock.
func filterAvailable(remotes []*availabilitypb.AvailabilityResponse, fromBlock uint64) []*availabilitypb.AvailabilityResponse {
	out := remotes[:0]
	for _, r := range remotes {
		if r == nil || !r.IsAvailable {
			continue
		}
		if r.Auth == nil || r.Auth.UUID == "" {
			continue
		}
		// BlockMerge is the server's current tip block number.
		if uint64(r.BlockMerge) < fromBlock {
			continue
		}
		out = append(out, r)
	}
	return out
}

// highestBlock returns the maximum BlockMerge (tip) across all availability responses.
func highestBlock(remotes []*availabilitypb.AvailabilityResponse) uint64 {
	var tip uint64
	for _, r := range remotes {
		if uint64(r.BlockMerge) > tip {
			tip = uint64(r.BlockMerge)
		}
	}
	return tip
}

// remoteToNodeinfo parses a types.Nodeinfo from an AvailabilityResponse.
func remoteToNodeinfo(r *availabilitypb.AvailabilityResponse) (*types.Nodeinfo, error) {
	if r == nil || r.Nodeinfo == nil {
		return nil, fmt.Errorf("nil availability response or nodeinfo")
	}

	var maddrs []multiaddr.Multiaddr
	for _, b := range r.Nodeinfo.Multiaddrs {
		ma, err := multiaddr.NewMultiaddrBytes(b)
		if err == nil {
			maddrs = append(maddrs, ma)
		}
	}

	pid, err := peer.IDFromBytes(r.Nodeinfo.PeerId)
	if err != nil {
		return nil, fmt.Errorf("parse peer ID: %w", err)
	}

	return &types.Nodeinfo{
		PeerID:    pid,
		Multiaddr: maddrs,
		Version:   uint16(r.Nodeinfo.Version),
	}, nil
}
