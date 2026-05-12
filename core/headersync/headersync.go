package headersync

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	checksum_priorsync "github.com/JupiterMetaLabs/JMDN-FastSync/common/checksum/checksum_priorsync"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	commonerrors "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
)

const (
	maxRetries    = 2
	maxSyncRounds = 4 // Max confirmation rounds to prevent infinite loops
	maxWorkers    = 3 // concurrent fetch workers
)

// batchJob is a unit of work sent to a fetch worker.
type batchJob struct {
	BatchID int
	Request *headersyncpb.HeaderSyncRequest
}

// batchResult is the outcome of a single batch fetch, sent from a worker to the writer.
type batchResult struct {
	BatchID int
	Headers []*blockpb.Header // nil on permanent failure
	Err     error             // non-nil when all remotes exhausted
}

type HeaderSync struct {
	SyncVars   *types.Syncvars
	Comm       communication.Communicator
	ServerAuth *authpb.Auth
}

func NewHeaderSync() *HeaderSync {
	return &HeaderSync{
		SyncVars: &types.Syncvars{},
	}
}

func (hs *HeaderSync) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) Headersync_router {
	if hs.SyncVars == nil {
		hs.SyncVars = &types.Syncvars{}
	}
	hs.Comm = communication.NewCommunication(node, protocolVersion)
	hs.SyncVars.Version = protocolVersion
	hs.SyncVars.NodeInfo = nodeInfo
	hs.SyncVars.Ctx = ctx
	hs.SyncVars.WAL = wal
	hs.SyncVars.Node = node
	return hs
}

func (hs *HeaderSync) GetSyncVars() *types.Syncvars {
	return hs.SyncVars
}

/*
- reason behind the remotes is that we can option to have multiple peers to sync headers from.
1. After getting the header request from the caller function, we have to iterate through the ranges.
2. Atmost import should be constants.MAX_HEADERS_PER_REQUEST.
3. we should select by ranges in the Tag.
  - if the selected ranges combined have more than 1500 headers then process it upto that chosen range but dont take new range.
  - if the selected ranges combined have less than 1500 headers then take new range.

4. On failure of a remote, we should try the next remote.
5. On failure of a range, we should retry the same range with the same remote for 3 times.
  - more than 3 times, then retry the same range with another remote.

6. Add the headers to the local database after successful receival. using nodeinfo.WriteHeaders.WriteHeaders(headers []*block.Header).
7. atlast we have to execute PRIORSYNC with the server to get to know are we fully synced or not.
*/
func (hs *HeaderSync) HeaderSync(headersyncrequest *headersyncpb.HeaderSyncRequest, remotes []*availabilitypb.AvailabilityResponse, syncConfirmation bool) (*datasyncpb.DataSyncRequest, error) {
	if headersyncrequest == nil {
		return nil, fmt.Errorf("headersync request or tag is nil")
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("no remotes provided")
	}
	if hs.Comm == nil {
		return nil, fmt.Errorf("communicator not set")
	}

	// Capture the original tag for later DataSyncRequest construction.
	originalTag := headersyncrequest.Tag

	if originalTag == nil {
		// No differences found — headers are already in sync.
		// When the successive phase is DATA_SYNC_REQUEST, it confirms
		// that the server verified both Merkle trees match.
		if headersyncrequest.Phase != nil && headersyncrequest.Phase.SuccessivePhase == constants.DATA_SYNC_REQUEST {
			Log.Logger(Log.HeaderSync).Debug(hs.SyncVars.Ctx, "Headers already in sync — proceeding to data sync",
				ion.String("successive_phase", headersyncrequest.Phase.SuccessivePhase))
		}
		return nil, nil
	}

	ctx := hs.SyncVars.Ctx
	headerWriter := hs.SyncVars.NodeInfo.BlockInfo.NewHeadersWriter()
	hs.ServerAuth = headersyncrequest.Phase.GetAuth()

	// ---------------------------------------------------------------
	// Initialize the queue with the first set of batches
	// ---------------------------------------------------------------
	queue := buildBatches(originalTag, hs.ServerAuth)

	Log.Logger(Log.HeaderSync).Debug(ctx, "HeaderSync starting",
		ion.Int("initial_batches", len(queue)),
		ion.Int("total_remotes", len(remotes)))

	if !syncConfirmation {
		// PoTS path: the server already identified exactly which blocks are
		// missing, so there is no need for a Merkle round-trip. Fetch the
		// headers once and return the DataSyncRequest immediately.
		if err := processQueue(ctx, hs, queue, remotes, headerWriter); err != nil {
			return nil, fmt.Errorf("header fetch failed: %w", err)
		}

		Log.Logger(Log.HeaderSync).Info(ctx, "HeaderSync completed — sync confirmation skipped (PoTS path)")

		return &datasyncpb.DataSyncRequest{
			Tag:     originalTag,
			Version: uint32(hs.SyncVars.Version),
			Ack: &ackpb.Ack{
				Ok:    true,
				Error: "",
			},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.HEADER_SYNC_RESPONSE,
				SuccessivePhase: constants.DATA_SYNC_REQUEST,
				Success:         true,
				Error:           "",
				Auth:            hs.ServerAuth,
			},
		}, nil
	}

	// ---------------------------------------------------------------
	// Normal FastSync path: process queue in rounds, then run
	// SyncConfirmation to compare Merkle trees. Repeat until synced.
	// ---------------------------------------------------------------
	for round := 1; round <= maxSyncRounds; round++ {
		Log.Logger(Log.HeaderSync).Debug(ctx, "Starting sync round",
			ion.Int("round", round),
			ion.Int("queue_size", len(queue)))

		// Drain all batches in the current queue (concurrently)
		if err := processQueue(ctx, hs, queue, remotes, headerWriter); err != nil {
			return nil, fmt.Errorf("round %d: %w", round, err)
		}

		// -------------------------------------------------------
		// Sync confirmation — compare Merkle trees with a remote
		// -------------------------------------------------------
		Log.Logger(Log.HeaderSync).Debug(ctx, "Running sync confirmation",
			ion.Int("round", round))

		newTag, synced, err := hs.SyncConfirmation(ctx, remotes)
		if err != nil {
			return nil, fmt.Errorf("round %d sync confirmation failed: %w", round, err)
		}

		if synced {
			Log.Logger(Log.HeaderSync).Info(ctx, "HeaderSync completed — trees match",
				ion.Int("rounds_taken", round))

			// Construct DataSyncRequest using the original identified tags.
			return &datasyncpb.DataSyncRequest{
				Tag:     originalTag,
				Version: uint32(hs.SyncVars.Version),
				Ack: &ackpb.Ack{
					Ok:    true,
					Error: "",
				},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.HEADER_SYNC_RESPONSE,
					SuccessivePhase: constants.DATA_SYNC_REQUEST,
					Success:         true,
					Error:           "",
					Auth:            hs.ServerAuth,
				},
			}, nil
		}

		// Trees still differ — enqueue the new batches for the next round
		queue = buildBatches(newTag, hs.ServerAuth)
		Log.Logger(Log.HeaderSync).Info(ctx, "Sync confirmation found differences, re-enqueuing",
			ion.Int("new_batches", len(queue)))
	}

	return nil, fmt.Errorf("header sync did not converge after %d rounds", maxSyncRounds)
}

// processQueue concurrently fetches header batches using a worker pool and
// writes the results to the DB via a single writer to preserve ordering.
func processQueue(
	ctx context.Context,
	hs *HeaderSync,
	queue []*headersyncpb.HeaderSyncRequest,
	remotes []*availabilitypb.AvailabilityResponse,
	headerWriter types.WriteHeaders,
) error {
	if len(queue) == 0 {
		return nil
	}

	// Size the worker pool: min(maxWorkers, num batches)
	// We do not cap by len(remotes) because a single remote can handle multiple concurrent batch requests.
	numWorkers := maxWorkers
	if len(queue) < numWorkers {
		numWorkers = len(queue)
	}

	Log.Logger(Log.HeaderSync).Info(ctx, "Worker pool starting",
		ion.Int("workers", numWorkers),
		ion.Int("batches", len(queue)))

	workCh := make(chan batchJob, len(queue))
	resultCh := make(chan batchResult, len(queue))

	// ---- Dispatcher: push all jobs into the work channel ----
	for i, req := range queue {
		workCh <- batchJob{BatchID: i + 1, Request: req}
	}
	close(workCh)

	// ---- Launch fetch workers ----
	var workerWg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		workerWg.Add(1)
		go func(workerID int) {
			defer workerWg.Done()
			fetchWorker(ctx, workerID, hs, remotes, workCh, resultCh)
		}(w + 1)
	}

	// Close resultCh once all workers finish
	go func() {
		workerWg.Wait()
		close(resultCh)
	}()

	// ---- Collect results ----
	var results []batchResult
	for r := range resultCh {
		results = append(results, r)
	}

	// ---- Check for errors ----
	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("batch %d failed: %w", r.BatchID, r.Err)
		}
	}

	// ---- Sort by first block number and write sequentially ----
	sort.Slice(results, func(i, j int) bool {
		if len(results[i].Headers) == 0 {
			return true
		}
		if len(results[j].Headers) == 0 {
			return false
		}
		return results[i].Headers[0].BlockNumber < results[j].Headers[0].BlockNumber
	})

	for _, r := range results {
		if len(r.Headers) == 0 {
			continue
		}

		// Write to WAL before DB — ensures crash recoverability
		if hs.SyncVars.WAL != nil {
			event := &WAL.HeaderSyncEvent{
				BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
				Response:  &headersyncpb.HeaderSyncResponse{Header: r.Headers},
			}
			lsn, err := hs.SyncVars.WAL.WriteEvent(event)
			if err != nil {
				return fmt.Errorf("batch %d: WAL write failed: %w", r.BatchID, err)
			}
			Log.Logger(Log.HeaderSync).Debug(ctx, "WAL event written",
				ion.Int("batch", r.BatchID),
				ion.Int64("lsn", int64(lsn)),
				ion.Int("headers", len(r.Headers)))

			if err := hs.SyncVars.WAL.Flush(); err != nil {
				return fmt.Errorf("batch %d: WAL flush failed: %w", r.BatchID, err)
			}
			Log.Logger(Log.HeaderSync).Debug(ctx, "WAL flushed",
				ion.Int("batch", r.BatchID),
				ion.Int64("last_flushed_lsn", int64(hs.SyncVars.WAL.GetLastFlushedLSN())))
		} else {
			Log.Logger(Log.HeaderSync).Warn(ctx, "WAL is nil — skipping WAL write",
				ion.Int("batch", r.BatchID))
		}

		if err := headerWriter.WriteHeaders(r.Headers); err != nil {
			return fmt.Errorf("batch %d: failed to write headers to DB: %w", r.BatchID, err)
		}

		Log.Logger(Log.HeaderSync).Info(ctx, "Batch written to DB",
			ion.Int("batch", r.BatchID),
			ion.Int("headers_written", len(r.Headers)),
			ion.Int64("first_block", int64(r.Headers[0].BlockNumber)),
			ion.Int64("last_block", int64(r.Headers[len(r.Headers)-1].BlockNumber)))

		if hs.SyncVars.WAL != nil {
			if _, err := hs.SyncVars.WAL.CreateCheckpoint(); err != nil {
				Log.Logger(Log.HeaderSync).Warn(ctx, "WAL checkpoint failed after header batch write",
					ion.Int("batch", r.BatchID),
					ion.Err(err))
			}
		}
	}

	return nil
}

// fetchWorker pulls jobs from workCh, tries each remote with retries, and
// sends results to resultCh. Implements retry-per-remote with failover.
func fetchWorker(
	ctx context.Context,
	workerID int,
	hs *HeaderSync,
	remotes []*availabilitypb.AvailabilityResponse,
	workCh <-chan batchJob,
	resultCh chan<- batchResult,
) {
	for job := range workCh {
		var lastErr error
		var headers []*blockpb.Header
		success := false

		for remoteIdx := 0; remoteIdx < len(remotes) && !success; remoteIdx++ {
			availResp := remotes[remoteIdx]
			remoteNodeInfo, err := helper.NewNodeInfoHelper().ToNodeinfo(availResp.Nodeinfo)
			if err != nil {
				lastErr = fmt.Errorf("worker %d, batch %d: failed to parse remote nodeinfo: %w",
					workerID, job.BatchID, err)
				continue
			}
			childctx, cancel := context.WithCancel(ctx)

			// Set the auth from the availability response for this specific remote
			if job.Request.Phase != nil {
				job.Request.Phase.Auth = availResp.Auth
			}

			for attempt := 1; attempt <= maxRetries; attempt++ {
				Log.Logger(Log.HeaderSync).Debug(childctx, "Worker sending header sync batch",
					ion.Int("worker", workerID),
					ion.Int("batch", job.BatchID),
					ion.Int("attempt", attempt),
					ion.String("peer", remoteNodeInfo.PeerID.String()))

				resp, err := hs.Comm.SendHeaderSyncRequest(childctx, *remoteNodeInfo, job.Request)
				if err != nil {
					lastErr = fmt.Errorf("worker %d, batch %d, remote %s, attempt %d: %w",
						workerID, job.BatchID, remoteNodeInfo.PeerID.String(), attempt, err)
					Log.Logger(Log.HeaderSync).Warn(childctx, "Header sync request failed",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				// Validate response
				if resp.Ack != nil && !resp.Ack.Ok {
					lastErr = fmt.Errorf("worker %d, batch %d: server returned error: %s",
						workerID, job.BatchID, resp.Ack.Error)
					Log.Logger(Log.HeaderSync).Warn(childctx, "Header sync response error",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				if len(resp.Header) == 0 {
					lastErr = fmt.Errorf("worker %d, batch %d: server returned 0 headers",
						workerID, job.BatchID)
					Log.Logger(Log.HeaderSync).Warn(childctx, "Empty header response",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				// Sort headers by block number
				sort.Slice(resp.Header, func(i, j int) bool {
					return resp.Header[i].BlockNumber < resp.Header[j].BlockNumber
				})

				headers = resp.Header
				success = true

				Log.Logger(Log.HeaderSync).Debug(childctx, "Batch fetched successfully",
					ion.Int("worker", workerID),
					ion.Int("batch", job.BatchID),
					ion.Int("headers_received", len(headers)),
					ion.Int64("first_block", int64(headers[0].BlockNumber)),
					ion.Int64("last_block", int64(headers[len(headers)-1].BlockNumber)))
				break
			}

			cancel()
		}

		if success {
			resultCh <- batchResult{BatchID: job.BatchID, Headers: headers}
		} else {
			resultCh <- batchResult{BatchID: job.BatchID, Err: lastErr}
		}
	}
}

// refreshedRemote holds a parsed node info and a freshly obtained auth token
// for a remote peer that has passed proactive re-authentication before a sync
// confirmation round.
type refreshedRemote struct {
	nodeInfo  *types.Nodeinfo
	freshAuth *authpb.Auth
}

// SyncConfirmation sends a PriorSync (Merkle comparison) request to each remote
// peer to determine whether local and remote header sets are in sync.
//
// Before comparing trees, it proactively refreshes the availability token for
// every remote in parallel, retrying up to maxRetries times per peer. Peers
// whose every attempt fails are excluded. If no peer can authenticate, the call
// returns commonerrors.AuthenticationFailed.
//
// Return semantics:
//   - (nil, true, nil)  — trees match; ready for DataSync.
//   - (tag, false, nil) — divergence detected; tag identifies the differing ranges.
//   - (nil, false, err) — unrecoverable error; caller must abort.

func (hs *HeaderSync) SyncConfirmation(ctx context.Context, remotes []*availabilitypb.AvailabilityResponse) (*taggingpb.Tag, bool, error) {
	localDetails := hs.SyncVars.NodeInfo.BlockInfo.GetBlockDetails()

	snapshot, err := hs.buildMerkleSnapshot(ctx, localDetails.Blocknumber)
	if err != nil {
		return nil, false, err
	}

	authRemotes, err := hs.refreshAuthForRemotes(ctx, remotes, localDetails.Blocknumber)
	if err != nil {
		return nil, false, err
	}

	return hs.sendPriorSyncToRemotes(ctx, snapshot, authRemotes, localDetails)
}

// buildMerkleSnapshot generates a Merkle tree over [0, topBlock] from the local
// block store and returns a serialized snapshot for inclusion in a PriorSync request.
func (hs *HeaderSync) buildMerkleSnapshot(ctx context.Context, topBlock uint64) (*merklepb.MerkleSnapshot, error) {
	db := merkle.NewMerkleProof(hs.SyncVars.NodeInfo.BlockInfo)
	builder, err := db.GenerateMerkleTree(ctx, 0, topBlock)
	if err != nil {
		return nil, fmt.Errorf("build merkle tree: %w", err)
	}
	snapshot, err := db.ToSnapshot(ctx, builder)
	if err != nil {
		return nil, fmt.Errorf("serialize merkle snapshot: %w", err)
	}
	return snapshot, nil
}

// refreshAuthForRemotes concurrently re-requests an availability token for each
// remote, delegating each attempt (with retries) to tryRefreshAuth. Peers that
// fail all retries are excluded. Returns commonerrors.AuthenticationFailed if every
// peer is excluded.
func (hs *HeaderSync) refreshAuthForRemotes(
	ctx context.Context,
	remotes []*availabilitypb.AvailabilityResponse,
	topBlock uint64,
) ([]refreshedRemote, error) {
	resultCh := make(chan refreshedRemote, len(remotes))
	var wg sync.WaitGroup

	for _, ar := range remotes {
		wg.Add(1)
		go func(ar *availabilitypb.AvailabilityResponse) {
			defer wg.Done()
			if r, ok := hs.tryRefreshAuth(ctx, ar, topBlock); ok {
				resultCh <- r
			}
		}(ar)
	}

	wg.Wait()
	close(resultCh)

	var out []refreshedRemote
	for r := range resultCh {
		out = append(out, r)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("sync confirmation: no peers could be re-authenticated: %w",
			commonerrors.AuthenticationFailed)
	}
	return out, nil
}

// tryRefreshAuth attempts to obtain a fresh availability token for a single peer,
// retrying up to maxRetries times. Returns (result, true) on success, (zero, false)
// when all attempts fail or the peer response is invalid.
func (hs *HeaderSync) tryRefreshAuth(
	ctx context.Context,
	ar *availabilitypb.AvailabilityResponse,
	topBlock uint64,
) (refreshedRemote, bool) {
	nodeInfo, err := helper.NewNodeInfoHelper().ToNodeinfo(ar.Nodeinfo)
	if err != nil {
		Log.Logger(Log.HeaderSync).Warn(ctx, "sync confirmation: cannot parse remote nodeinfo",
			ion.Err(err))
		return refreshedRemote{}, false
	}

	req := &availabilitypb.AvailabilityRequest{
		Range: &merklepb.Range{Start: 0, End: topBlock},
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := hs.Comm.SendAvailabilityRequest(ctx, *nodeInfo, req)
		if err == nil && resp != nil && resp.IsAvailable && resp.Auth != nil && resp.Auth.UUID != "" {
			// Mutate ar.Auth in-place so the original remotes slice entry reflects
			// the fresh token. Workers in processQueue read availResp.Auth directly,
			// so this propagates without any additional bookkeeping.
			ar.Auth = resp.Auth
			Log.Logger(Log.HeaderSync).Debug(ctx, "sync confirmation: re-auth ok",
				ion.String("peer", nodeInfo.PeerID.String()),
				ion.String("uuid", resp.Auth.UUID))
			return refreshedRemote{nodeInfo: nodeInfo, freshAuth: resp.Auth}, true
		}
		Log.Logger(Log.HeaderSync).Warn(ctx, "sync confirmation: re-auth attempt failed",
			ion.String("peer", nodeInfo.PeerID.String()),
			ion.Int("attempt", attempt),
			ion.Err(err))
	}

	Log.Logger(Log.HeaderSync).Warn(ctx, "sync confirmation: re-auth exhausted for peer",
		ion.String("peer", nodeInfo.PeerID.String()))
	return refreshedRemote{}, false
}

// sendPriorSyncToRemotes iterates over authenticated remotes, sending a PriorSync
// Merkle comparison to each in turn. Returns on the first definitive outcome.
func (hs *HeaderSync) sendPriorSyncToRemotes(
	ctx context.Context,
	snapshot *merklepb.MerkleSnapshot,
	remotes []refreshedRemote,
	localDetails types.PriorSync,
) (*taggingpb.Tag, bool, error) {
	blockRange := priorSyncRange(localDetails)

	for _, remote := range remotes {
		msg, err := buildPriorSyncMsg(localDetails, blockRange, remote.freshAuth)
		if err != nil {
			return nil, false, err
		}

		Log.Logger(Log.HeaderSync).Debug(ctx, "sync confirmation: sending priorsync",
			ion.String("peer", remote.nodeInfo.PeerID.String()),
			ion.Uint64("blocknumber", localDetails.Blocknumber),
			ion.String("uuid", msg.Phase.GetAuth().GetUUID()))

		childCtx, cancel := context.WithCancel(ctx)
		resp, err := hs.Comm.SendPriorSync(childCtx, snapshot, *remote.nodeInfo, *msg)
		cancel() // called directly to avoid defer accumulation in a loop

		if err != nil {
			Log.Logger(Log.HeaderSync).Warn(ctx, "sync confirmation: send failed, trying next peer",
				ion.String("peer", remote.nodeInfo.PeerID.String()),
				ion.Err(err))
			continue
		}

		tag, synced, err := interpretSyncResponse(ctx, resp, remote.nodeInfo)
		if err != nil {
			return nil, false, err
		}
		if synced || tag != nil {
			// Update ServerAuth to the token of the peer that gave a definitive
			// response. This ensures buildBatches and the DataSyncRequest envelope
			// always carry the auth of the peer that actually confirmed the state,
			// not an arbitrary index from the re-auth fan-out.
			hs.ServerAuth = remote.freshAuth
			return tag, synced, nil
		}
		// Ambiguous response: try the next authenticated peer.
	}

	return nil, false, fmt.Errorf("sync confirmation: all peers exhausted without a definitive response")
}

// priorSyncRange returns the Merkle comparison range to embed in a PriorSync message.
// If the local block state carries a bounded range, that range is preserved;
// otherwise the full chain range [0, topBlock] is used.
func priorSyncRange(details types.PriorSync) *merklepb.Range {
	if details.Range != nil {
		return &merklepb.Range{Start: details.Range.Start, End: details.Range.End}
	}
	return &merklepb.Range{Start: 0, End: details.Blocknumber}
}

// buildPriorSyncMsg constructs a PriorSyncMessage with a computed checksum.
// It is a pure function: same inputs always produce equivalent output.
func buildPriorSyncMsg(
	details types.PriorSync,
	blockRange *merklepb.Range,
	auth *authpb.Auth,
) (*priorsyncpb.PriorSyncMessage, error) {
	msg := &priorsyncpb.PriorSyncMessage{
		Priorsync: &priorsyncpb.PriorSync{
			Blocknumber: details.Blocknumber,
			Stateroot:   details.Stateroot,
			Blockhash:   details.Blockhash,
			Range:       blockRange,
			Metadata:    &priorsyncpb.Metadata{},
		},
		Phase: &phasepb.Phase{Auth: auth},
	}

	checksum, err := checksum_priorsync.PriorSyncChecksum().CreatefromPB(
		msg.GetPriorsync(), uint16(details.Metadata.Version),
	)
	if err != nil {
		return nil, fmt.Errorf("compute priorsync checksum: %w", err)
	}

	msg.Priorsync.Metadata.Checksum = checksum
	msg.Priorsync.Metadata.Version = uint32(details.Metadata.Version)
	return msg, nil
}

// interpretSyncResponse classifies a PriorSync response from a remote peer:
//   - (nil, true, nil)  — Merkle trees match; ready for DataSync.
//   - (tag, false, nil) — divergence detected; tag holds the differing ranges.
//   - (nil, false, err) — structurally invalid success response; caller must abort.
//   - (nil, false, nil) — ambiguous response; caller should try the next peer.
func interpretSyncResponse(
	ctx context.Context,
	resp *priorsyncpb.PriorSyncMessage,
	peer *types.Nodeinfo,
) (*taggingpb.Tag, bool, error) {
	if resp.Phase != nil && resp.Phase.Success {
		if resp.Headersync == nil {
			return nil, false, fmt.Errorf("sync confirmation: success response missing headersync payload")
		}
		if resp.Headersync.Tag == nil && resp.Phase.SuccessivePhase == constants.DATA_SYNC_REQUEST {
			Log.Logger(Log.HeaderSync).Info(ctx, "sync confirmation: trees match, advancing to data sync",
				ion.String("peer", peer.PeerID.String()))
			return nil, true, nil
		}
	}

	if resp.Headersync != nil && resp.Headersync.Tag != nil {
		tag := resp.Headersync.Tag
		Log.Logger(Log.HeaderSync).Info(ctx, "sync confirmation: divergence detected",
			ion.String("peer", peer.PeerID.String()),
			ion.Int("ranges", len(tag.Range)),
			ion.Int("blocks", len(tag.BlockNumber)))
		return tag, false, nil
	}

	Log.Logger(Log.HeaderSync).Warn(ctx, "sync confirmation: ambiguous response from peer",
		ion.String("peer", peer.PeerID.String()))
	return nil, false, nil
}

// buildBatches groups tag ranges and individual block numbers into
// HeaderSyncRequest batches, each containing at most MAX_HEADERS_PER_REQUEST headers.
func buildBatches(tag *taggingpb.Tag, auth *authpb.Auth) []*headersyncpb.HeaderSyncRequest {
	maxPerBatch := uint64(constants.MAX_HEADERS_PER_REQUEST)
	var batches []*headersyncpb.HeaderSyncRequest

	// Sort ranges by start for deterministic ordering
	ranges := make([]*taggingpb.RangeTag, len(tag.Range))
	copy(ranges, tag.Range)
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].Start < ranges[j].Start
	})

	// Sort individual block numbers
	blockNums := make([]uint64, len(tag.BlockNumber))
	copy(blockNums, tag.BlockNumber)
	sort.Slice(blockNums, func(i, j int) bool {
		return blockNums[i] < blockNums[j]
	})

	// -- Pack ranges into batches --
	currentTag := &taggingpb.Tag{}
	var currentCount uint64

	for _, r := range ranges {
		rangeSize := r.End - r.Start + 1

		// If adding this range would exceed the limit, finalize the current batch
		// (but always include at least one range per batch)
		if currentCount > 0 && currentCount+rangeSize > maxPerBatch {
			batches = append(batches, makeBatchRequest(currentTag, auth))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}

		currentTag.Range = append(currentTag.Range, r)
		currentCount += rangeSize

		// If this single range already exceeds the limit, finalize immediately
		if currentCount >= maxPerBatch {
			batches = append(batches, makeBatchRequest(currentTag, auth))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}
	}

	// -- Pack individual block numbers into the current or new batch --
	for _, bn := range blockNums {
		if currentCount > 0 && currentCount+1 > maxPerBatch {
			batches = append(batches, makeBatchRequest(currentTag, auth))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}

		currentTag.BlockNumber = append(currentTag.BlockNumber, bn)
		currentCount++
	}

	// Flush remaining
	if currentCount > 0 {
		batches = append(batches, makeBatchRequest(currentTag, auth))
	}

	return batches
}

// makeBatchRequest wraps a Tag into a HeaderSyncRequest with proper phase info.
func makeBatchRequest(tag *taggingpb.Tag, auth *authpb.Auth) *headersyncpb.HeaderSyncRequest {
	return &headersyncpb.HeaderSyncRequest{
		Tag: tag,
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.HEADER_SYNC_REQUEST,
			SuccessivePhase: constants.HEADER_SYNC_RESPONSE,
			Success:         true,
			Error:           "",
			Auth:            auth,
		},
	}
}

func (hs *HeaderSync) Close() {
	hs.SyncVars.Ctx.Done()
	hs.SyncVars = nil
	hs.Comm = nil
}
