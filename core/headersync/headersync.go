package headersync

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
)

const (
	namedlogger   = Log.HeaderSync
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
	SyncVars *types.Syncvars
	Comm     communication.Communicator
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
func (hs *HeaderSync) HeaderSync(headersyncrequest *headersyncpb.HeaderSyncRequest, remotes []*types.Nodeinfo) (*datasyncpb.DataSyncRequest, error) {
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
			Log.Logger(namedlogger).Info(hs.SyncVars.Ctx, "Headers already in sync — proceeding to data sync",
				ion.String("successive_phase", headersyncrequest.Phase.SuccessivePhase))
		}
		return nil, nil
	}

	ctx := hs.SyncVars.Ctx
	headerWriter := hs.SyncVars.NodeInfo.BlockInfo.NewHeadersWriter()

	// ---------------------------------------------------------------
	// Initialize the queue with the first set of batches
	// ---------------------------------------------------------------
	queue := buildBatches(originalTag)

	Log.Logger(namedlogger).Info(ctx, "HeaderSync starting",
		ion.Int("initial_batches", len(queue)),
		ion.Int("total_remotes", len(remotes)))

	// ---------------------------------------------------------------
	// Process queue in rounds. Each round drains the queue, then
	// runs sync_confirmation. If still out of sync, new batches are
	// enqueued and the next round begins.
	// ---------------------------------------------------------------
	for round := 1; round <= maxSyncRounds; round++ {
		Log.Logger(namedlogger).Info(ctx, "Starting sync round",
			ion.Int("round", round),
			ion.Int("queue_size", len(queue)))

		// Drain all batches in the current queue (concurrently)
		if err := processQueue(ctx, hs, queue, remotes, headerWriter); err != nil {
			return nil, fmt.Errorf("round %d: %w", round, err)
		}

		// -------------------------------------------------------
		// Sync confirmation — compare Merkle trees with a remote
		// -------------------------------------------------------
		Log.Logger(namedlogger).Info(ctx, "Running sync confirmation",
			ion.Int("round", round))

		newTag, synced, err := hs.SyncConfirmation(ctx, remotes)
		if err != nil {
			return nil, fmt.Errorf("round %d sync confirmation failed: %w", round, err)
		}

		if synced {
			Log.Logger(namedlogger).Info(ctx, "HeaderSync completed — trees match",
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
				},
			}, nil
		}

		// Trees still differ — enqueue the new batches for the next round
		queue = buildBatches(newTag)
		Log.Logger(namedlogger).Info(ctx, "Sync confirmation found differences, re-enqueuing",
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
	remotes []*types.Nodeinfo,
	headerWriter types.WriteHeaders,
) error {
	if len(queue) == 0 {
		return nil
	}

	// Size the worker pool: min(maxWorkers, num remotes, num batches)
	numWorkers := maxWorkers
	if len(remotes) < numWorkers {
		numWorkers = len(remotes)
	}
	if len(queue) < numWorkers {
		numWorkers = len(queue)
	}

	Log.Logger(namedlogger).Info(ctx, "Worker pool starting",
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
			Log.Logger(namedlogger).Info(ctx, "WAL event written",
				ion.Int("batch", r.BatchID),
				ion.Int64("lsn", int64(lsn)),
				ion.Int("headers", len(r.Headers)))

			if err := hs.SyncVars.WAL.Flush(); err != nil {
				return fmt.Errorf("batch %d: WAL flush failed: %w", r.BatchID, err)
			}
			Log.Logger(namedlogger).Info(ctx, "WAL flushed",
				ion.Int("batch", r.BatchID),
				ion.Int64("last_flushed_lsn", int64(hs.SyncVars.WAL.GetLastFlushedLSN())))
		} else {
			Log.Logger(namedlogger).Warn(ctx, "WAL is nil — skipping WAL write",
				ion.Int("batch", r.BatchID))
		}

		if err := headerWriter.WriteHeaders(r.Headers); err != nil {
			return fmt.Errorf("batch %d: failed to write headers to DB: %w", r.BatchID, err)
		}

		Log.Logger(namedlogger).Info(ctx, "Batch written to DB",
			ion.Int("batch", r.BatchID),
			ion.Int("headers_written", len(r.Headers)),
			ion.Int64("first_block", int64(r.Headers[0].BlockNumber)),
			ion.Int64("last_block", int64(r.Headers[len(r.Headers)-1].BlockNumber)))
	}

	return nil
}

// fetchWorker pulls jobs from workCh, tries each remote with retries, and
// sends results to resultCh. Implements retry-per-remote with failover.
func fetchWorker(
	ctx context.Context,
	workerID int,
	hs *HeaderSync,
	remotes []*types.Nodeinfo,
	workCh <-chan batchJob,
	resultCh chan<- batchResult,
) {
	for job := range workCh {
		var lastErr error
		var headers []*blockpb.Header
		success := false

		for remoteIdx := 0; remoteIdx < len(remotes) && !success; remoteIdx++ {
			remote := remotes[remoteIdx]
			childctx, cancel := context.WithCancel(ctx)

			for attempt := 1; attempt <= maxRetries; attempt++ {
				Log.Logger(namedlogger).Debug(childctx, "Worker sending header sync batch",
					ion.Int("worker", workerID),
					ion.Int("batch", job.BatchID),
					ion.Int("attempt", attempt),
					ion.String("peer", remote.PeerID.String()))

				resp, err := hs.Comm.SendHeaderSyncRequest(childctx, *remote, job.Request)
				if err != nil {
					lastErr = fmt.Errorf("worker %d, batch %d, remote %s, attempt %d: %w",
						workerID, job.BatchID, remote.PeerID.String(), attempt, err)
					Log.Logger(namedlogger).Warn(childctx, "Header sync request failed",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				// Validate response
				if resp.Ack != nil && !resp.Ack.Ok {
					lastErr = fmt.Errorf("worker %d, batch %d: server returned error: %s",
						workerID, job.BatchID, resp.Ack.Error)
					Log.Logger(namedlogger).Warn(childctx, "Header sync response error",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				if len(resp.Header) == 0 {
					lastErr = fmt.Errorf("worker %d, batch %d: server returned 0 headers",
						workerID, job.BatchID)
					Log.Logger(namedlogger).Warn(childctx, "Empty header response",
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

				Log.Logger(namedlogger).Info(childctx, "Batch fetched successfully",
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

// syncConfirmation sends a PriorSync (SYNC_REQUEST) to a remote to compare
// Merkle trees. If the trees match, (nil, true, nil) is returned. If they differ,
// the response will contain a HeaderSyncRequest.Tag with the differing ranges,
// which is returned as (tag, false, nil) for re-enqueueing.
func (hs *HeaderSync) SyncConfirmation(ctx context.Context, remotes []*types.Nodeinfo) (*taggingpb.Tag, bool, error) {
	// Build local Merkle tree from our current block state
	blockInfo := hs.SyncVars.NodeInfo.BlockInfo
	localDetails := blockInfo.GetBlockDetails()

	// Build the local Merkle snapshot
	merkleDb := merkle.NewMerkleProof(blockInfo)
	merklebuilder, err := merkleDb.GenerateMerkleTree(context.Background(), 0, localDetails.Blocknumber)
	if err != nil {
		return nil, false, fmt.Errorf("failed to build local merkle tree: %w", err)
	}
	merkleSnapshot, err := merkleDb.ToSnapshot(ctx, merklebuilder)
	if err != nil {
		return nil, false, fmt.Errorf("failed to convert merkle snapshot: %w", err)
	}
	// Try each remote for confirmation
	for _, remote := range remotes {
		childctx, cancel := context.WithCancel(ctx)

		syncMsg := types.PriorSyncMessage{
			Priorsync: &types.PriorSync{
				Blocknumber: localDetails.Blocknumber,
				Stateroot:   localDetails.Stateroot,
				Blockhash:   localDetails.Blockhash,
				Metadata:    localDetails.Metadata,
				Range:       localDetails.Range,
			},
		}

		resp, err := hs.Comm.SendPriorSync(childctx, merkleSnapshot, *remote, syncMsg)
		cancel()

		if err != nil {
			Log.Logger(namedlogger).Warn(ctx, "Sync confirmation failed with remote, trying next",
				ion.String("peer", remote.PeerID.String()),
				ion.Err(err))
			continue
		}

		// Check the response phase for success
		if resp.Phase != nil && resp.Phase.Success {
			if resp.Headersync == nil {
				return nil, false, fmt.Errorf("sync confirmation failed: headersync is nil")
			}
			// Trees match — nil tag with successive phase DATA_SYNC_REQUEST
			// confirms headers are in sync.
			if resp.Headersync.Tag == nil && resp.Phase.SuccessivePhase == constants.DATA_SYNC_REQUEST {
				Log.Logger(namedlogger).Info(ctx, "Sync confirmation: headers in sync, ready for data sync",
					ion.String("successive_phase", resp.Phase.SuccessivePhase))
				return nil, true, nil
			}
		}

		// Trees differ — server returned tagged ranges that still need syncing
		if resp.Headersync != nil && resp.Headersync.Tag != nil {
			tag := resp.Headersync.Tag
			totalRanges := len(tag.Range)
			totalBlocks := len(tag.BlockNumber)
			Log.Logger(namedlogger).Info(ctx, "Sync confirmation: still out of sync",
				ion.Int("remaining_ranges", totalRanges),
				ion.Int("remaining_blocks", totalBlocks))
			return tag, false, nil
		}

		// No phase success and no tag — ambiguous, try next remote
		Log.Logger(namedlogger).Warn(ctx, "Sync confirmation returned ambiguous response",
			ion.String("peer", remote.PeerID.String()))
	}

	return nil, false, fmt.Errorf("sync confirmation failed: all remotes exhausted")
}

// buildBatches groups tag ranges and individual block numbers into
// HeaderSyncRequest batches, each containing at most MAX_HEADERS_PER_REQUEST headers.
func buildBatches(tag *taggingpb.Tag) []*headersyncpb.HeaderSyncRequest {
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
			batches = append(batches, makeBatchRequest(currentTag))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}

		currentTag.Range = append(currentTag.Range, r)
		currentCount += rangeSize

		// If this single range already exceeds the limit, finalize immediately
		if currentCount >= maxPerBatch {
			batches = append(batches, makeBatchRequest(currentTag))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}
	}

	// -- Pack individual block numbers into the current or new batch --
	for _, bn := range blockNums {
		if currentCount > 0 && currentCount+1 > maxPerBatch {
			batches = append(batches, makeBatchRequest(currentTag))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}

		currentTag.BlockNumber = append(currentTag.BlockNumber, bn)
		currentCount++
	}

	// Flush remaining
	if currentCount > 0 {
		batches = append(batches, makeBatchRequest(currentTag))
	}

	return batches
}

// makeBatchRequest wraps a Tag into a HeaderSyncRequest with proper phase info.
func makeBatchRequest(tag *taggingpb.Tag) *headersyncpb.HeaderSyncRequest {
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
		},
	}
}

func (hs *HeaderSync) Close() {
	hs.SyncVars.Ctx.Done()
	hs.SyncVars = nil
	hs.Comm = nil
}
