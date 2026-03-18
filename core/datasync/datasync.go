package datasync

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/tagging"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
)

const (
	maxRetries = 2
	maxWorkers = 3
)

// dataBatchJob is a unit of work sent to a fetch worker.
type dataBatchJob struct {
	BatchID int
	Request *datasyncpb.DataSyncRequest
}

// dataBatchResult is the outcome of a single batch fetch.
type dataBatchResult struct {
	BatchID        int
	NonHeaders     []*blockpb.NonHeaders     // nil on permanent failure
	TaggedAccounts *taggingpb.TaggedAccounts // nil on permanent failure
	Err            error                     // non-nil when all remotes exhausted
}

type DataSync struct {
	SyncVars   *types.Syncvars
	Comm       communication.Communicator
	ServerAuth *authpb.Auth
}

func NewDataSync() *DataSync {
	return &DataSync{}
}

func (ds *DataSync) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) DataSync_router {
	if ds.SyncVars == nil {
		ds.SyncVars = &types.Syncvars{}
	}
	ds.Comm = communication.NewCommunication(node, protocolVersion)
	ds.SyncVars.Version = protocolVersion
	ds.SyncVars.NodeInfo = nodeInfo
	ds.SyncVars.Ctx = ctx
	ds.SyncVars.WAL = wal
	ds.SyncVars.Node = node
	return ds
}

func (ds *DataSync) GetSyncVars() *types.Syncvars {
	return ds.SyncVars
}

/*
DataSync fetches non-header block data from remote peers and writes it to the local DB.

1. Split the incoming Tag into batches of at most MAX_DATA_PER_REQUEST blocks.
2. Dispatch batches to a worker pool that queries remotes with retry + failover.
3. Collect results, sort by block number, and write to DB via WriteData.
*/
func (ds *DataSync) DataSync(datasyncrequest *datasyncpb.DataSyncRequest, remotes []*availabilitypb.AvailabilityResponse) (*taggingpb.TaggedAccounts, error) {
	if datasyncrequest == nil || datasyncrequest.Tag == nil {
		return nil, fmt.Errorf("datasync request or tag is nil")
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("no remotes provided")
	}
	if ds.Comm == nil {
		return nil, fmt.Errorf("communicator not set")
	}

	ctx := ds.SyncVars.Ctx
	ds.ServerAuth = datasyncrequest.Phase.GetAuth()

	// ---------------------------------------------------------------
	// Build batches from the tag (at most MAX_DATA_PER_REQUEST per batch)
	// ---------------------------------------------------------------
	queue := buildDataBatches(datasyncrequest.Tag, ds.SyncVars.Version, ds.ServerAuth)

	Log.Logger(Log.DataSync).Debug(ctx, "DataSync starting",
		ion.Int("initial_batches", len(queue)),
		ion.Int("total_remotes", len(remotes)))

	// ---------------------------------------------------------------
	// Process the queue concurrently
	// ---------------------------------------------------------------
	tags, err := processDataQueue(ctx, ds, queue, remotes)
	if err != nil {
		return nil, fmt.Errorf("datasync failed: %w", err)
	}

	Log.Logger(Log.DataSync).Info(ctx, "DataSync completed successfully")
	return tags, nil
}

// processDataQueue concurrently fetches non-header batches using a worker pool
// and writes the results to the DB via a writer to preserve ordering.
func processDataQueue(
	ctx context.Context,
	ds *DataSync,
	queue []*datasyncpb.DataSyncRequest,
	remotes []*availabilitypb.AvailabilityResponse,
) (*taggingpb.TaggedAccounts, error) {
	if len(queue) == 0 {
		return nil, nil
	}

	// Create a cancellable context for early termination on error
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers() // Always clean up

	// Size the worker pool: min(maxWorkers, num batches)
	// We do not cap by len(remotes) because a single remote can handle multiple concurrent batch requests.
	numWorkers := min(maxWorkers, len(queue))

	Log.Logger(Log.DataSync).Debug(ctx, "Worker pool starting",
		ion.Int("workers", numWorkers),
		ion.Int("batches", len(queue)))

	workCh := make(chan dataBatchJob, len(queue))
	resultCh := make(chan dataBatchResult, len(queue))

	// ---- Dispatcher: push all jobs into the work channel ----
	for i, req := range queue {
		workCh <- dataBatchJob{BatchID: i + 1, Request: req}
	}
	close(workCh)

	// ---- Launch fetch workers ----
	var workerWg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		workerWg.Add(1)
		go func(workerID int) {
			defer workerWg.Done()
			dataFetchWorker(workerCtx, workerID, ds, remotes, workCh, resultCh)
		}(w + 1)
	}

	// Close resultCh once all workers finish
	go func() {
		workerWg.Wait()
		close(resultCh)
	}()

	// ---- Collect and Stream Results ----
	expectedBatchID := 1
	outOfOrderBatches := make(map[int]dataBatchResult)
	aggregatedTags := tagging.NewTagging()

	for r := range resultCh {
		if r.Err != nil {
			// Cancel workers immediately to stop ongoing fetches
			cancelWorkers()
			return nil, fmt.Errorf("batch %d failed: %w", r.BatchID, r.Err)
		}

		if r.BatchID == expectedBatchID {
			// Expected sequence batch -> Process & Write directly
			if err := ds.processAndWriteBatch(ctx, r, aggregatedTags); err != nil {
				// Cancel workers on processing error
				cancelWorkers()
				return nil, err
			}
			expectedBatchID++

			// Drain map loop incrementally if following items are queued
			for {
				if nextBatch, queued := outOfOrderBatches[expectedBatchID]; queued {
					if err := ds.processAndWriteBatch(ctx, nextBatch, aggregatedTags); err != nil {
						cancelWorkers()
						return nil, err
					}
					// Remove to immediately dereference objects allowing Memory GC!
					delete(outOfOrderBatches, expectedBatchID)
					expectedBatchID++
				} else {
					break
				}
			}
		} else {
			// Buffer the out of order batch since it has completed faster than an earlier item
			outOfOrderBatches[r.BatchID] = r
		}
	}

	return aggregatedTags.GetAccountTag(), nil
}

// processAndWriteBatch performs the extraction and storage of a particular dataSync batch iteration natively.
func (ds *DataSync) processAndWriteBatch(ctx context.Context, r dataBatchResult, aggregatedTags *tagging.Tagging) error {
	// Aggregate tags mapping natively
	if r.TaggedAccounts != nil && r.TaggedAccounts.Accounts != nil {
		for addr := range r.TaggedAccounts.Accounts {
			aggregatedTags.TagAccounts(addr)
		}
	}

	if len(r.NonHeaders) == 0 {
		return nil
	}

	// Write to WAL before DB — ensures crash recoverability
	if ds.SyncVars.WAL != nil {
		event := &WAL.DataSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Response:  &datasyncpb.DataSyncResponse{Data: r.NonHeaders},
		}
		lsn, err := ds.SyncVars.WAL.WriteEvent(event)
		if err != nil {
			return fmt.Errorf("batch %d: WAL write failed: %w", r.BatchID, err)
		}
		Log.Logger(Log.DataSync).Debug(ctx, "WAL event written",
			ion.Int("batch", r.BatchID),
			ion.Int64("lsn", int64(lsn)),
			ion.Int("nonheaders", len(r.NonHeaders)))

		if err := ds.SyncVars.WAL.Flush(); err != nil {
			return fmt.Errorf("batch %d: WAL flush failed: %w", r.BatchID, err)
		}
		Log.Logger(Log.DataSync).Debug(ctx, "WAL flushed",
			ion.Int("batch", r.BatchID),
			ion.Int64("last_flushed_lsn", int64(ds.SyncVars.WAL.GetLastFlushedLSN())))
	} else {
		Log.Logger(Log.DataSync).Warn(ctx, "WAL is nil — skipping WAL write",
			ion.Int("batch", r.BatchID))
	}

	if err := ds.SyncVars.NodeInfo.BlockInfo.NewDataWriter().WriteData(r.NonHeaders); err != nil {
		return fmt.Errorf("batch %d: failed to write non-headers to DB: %w", r.BatchID, err)
	}

	// Immediate WAL checkpoint sequence!
	if ds.SyncVars.WAL != nil {
		checkpointStartTime := time.Now()
		if _, err := ds.SyncVars.WAL.CreateCheckpoint(); err != nil {
			checkpointEndTime := time.Now()
			Log.Logger(Log.DataSync).Warn(ctx, "failed to create WAL checkpoint",
				ion.Int("batch", r.BatchID),
				ion.Err(err),
				ion.String("duration", checkpointEndTime.Sub(checkpointStartTime).String()))
		} else {
			checkpointEndTime := time.Now()
			Log.Logger(Log.DataSync).Debug(ctx, "WAL checkpoint created",
				ion.Int("batch", r.BatchID),
				ion.String("duration", checkpointEndTime.Sub(checkpointStartTime).String()))
		}
	}

	Log.Logger(Log.DataSync).Info(ctx, "Batch written to DB",
		ion.Int("batch", r.BatchID),
		ion.Int("nonheaders_written", len(r.NonHeaders)),
		ion.Int64("first_block", int64(r.NonHeaders[0].BlockNumber)),
		ion.Int64("last_block", int64(r.NonHeaders[len(r.NonHeaders)-1].BlockNumber)))

	return nil
}

// dataFetchWorker pulls jobs from workCh, tries each remote with retries, and
// sends results to resultCh. Implements retry-per-remote with failover.
func dataFetchWorker(
	ctx context.Context,
	workerID int,
	ds *DataSync,
	remotes []*availabilitypb.AvailabilityResponse,
	workCh <-chan dataBatchJob,
	resultCh chan<- dataBatchResult,
) {
	for job := range workCh {
		var lastErr error
		var nonHeaders []*blockpb.NonHeaders
		var taggedAccounts *taggingpb.TaggedAccounts
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
			defer cancel()

			// Create a copy of the request with the auth from the availability response for this specific remote
			batchRequest := job.Request
			if batchRequest.Phase != nil && availResp.Auth != nil {
				// Create a shallow copy of the request to avoid mutation races
				requestCopy := *batchRequest
				phaseCopy := *batchRequest.Phase
				phaseCopy.Auth = availResp.Auth
				requestCopy.Phase = &phaseCopy
				batchRequest = &requestCopy
			}

			for attempt := 1; attempt <= maxRetries; attempt++ {
				Log.Logger(Log.DataSync).Debug(childctx, "Worker sending data sync batch",
					ion.Int("worker", workerID),
					ion.Int("batch", job.BatchID),
					ion.Int("attempt", attempt),
					ion.String("peer", remoteNodeInfo.PeerID.String()))

				resp, err := ds.Comm.SendDataSyncRequest(childctx, *remoteNodeInfo, batchRequest)
				if err != nil {
					lastErr = fmt.Errorf("worker %d, batch %d, remote %s, attempt %d: %w",
						workerID, job.BatchID, remoteNodeInfo.PeerID.String(), attempt, err)
					Log.Logger(Log.DataSync).Warn(childctx, "Data sync request failed",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				// Validate response
				if resp.Ack != nil && !resp.Ack.Ok {
					lastErr = fmt.Errorf("worker %d, batch %d: server returned error: %s",
						workerID, job.BatchID, resp.Ack.Error)
					Log.Logger(Log.DataSync).Warn(childctx, "Data sync response error",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				if len(resp.Data) == 0 {
					lastErr = fmt.Errorf("worker %d, batch %d: server returned 0 non-headers",
						workerID, job.BatchID)
					Log.Logger(Log.DataSync).Warn(childctx, "Empty data sync response",
						ion.Err(lastErr),
						ion.Int("worker", workerID),
						ion.Int("attempt", attempt))
					continue
				}

				// Sort non-headers by block number
				sort.Slice(resp.Data, func(i, j int) bool {
					return resp.Data[i].BlockNumber < resp.Data[j].BlockNumber
				})

				nonHeaders = resp.Data
				taggedAccounts = resp.Taggedaccounts
				success = true

				Log.Logger(Log.DataSync).Debug(childctx, "Batch fetched successfully",
					ion.Int("worker", workerID),
					ion.Int("batch", job.BatchID),
					ion.Int("nonheaders_received", len(nonHeaders)),
					ion.Int64("first_block", int64(nonHeaders[0].BlockNumber)),
					ion.Int64("last_block", int64(nonHeaders[len(nonHeaders)-1].BlockNumber)))
				break
			}
		}

		if success {
			resultCh <- dataBatchResult{BatchID: job.BatchID, NonHeaders: nonHeaders, TaggedAccounts: taggedAccounts}
		} else {
			resultCh <- dataBatchResult{BatchID: job.BatchID, Err: lastErr}
		}
	}
}

// buildDataBatches groups tag ranges and individual block numbers into
// DataSyncRequest batches, each containing at most MAX_DATA_PER_REQUEST blocks.
func buildDataBatches(tag *taggingpb.Tag, version uint16, auth *authpb.Auth) []*datasyncpb.DataSyncRequest {
	maxPerBatch := uint64(constants.MAX_DATA_PER_REQUEST)
	var batches []*datasyncpb.DataSyncRequest

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
		currStart := r.Start
		currEnd := r.End

		// Keep pulling chunks from this range until we've processed it all
		for currStart <= currEnd {
			remainingCapacity := maxPerBatch - currentCount
			remainingPieces := currEnd - currStart + 1

			if remainingPieces <= remainingCapacity {
				// The rest of this range fits entirely in the current batch
				currentTag.Range = append(currentTag.Range, &taggingpb.RangeTag{Start: currStart, End: currEnd})
				currentCount += remainingPieces
				currStart = currEnd + 1 // Done with this range

				// If the batch exactly hit capacity, flush it
				if currentCount == maxPerBatch {
					batches = append(batches, makeDataBatchRequest(currentTag, version, auth))
					currentTag = &taggingpb.Tag{}
					currentCount = 0
				}
			} else {
				// We can only fit `remainingCapacity` from this range into the current batch
				chunkEnd := currStart + remainingCapacity - 1
				currentTag.Range = append(currentTag.Range, &taggingpb.RangeTag{Start: currStart, End: chunkEnd})
				currentCount += remainingCapacity

				// Batch is now full, finalize it
				batches = append(batches, makeDataBatchRequest(currentTag, version, auth))
				currentTag = &taggingpb.Tag{}
				currentCount = 0

				// Advance currStart sequentially for the next sub-range
				currStart = chunkEnd + 1
			}
		}
	}

	// -- Pack individual block numbers into the current or new batch --
	for _, bn := range blockNums {
		if currentCount > 0 && currentCount+1 > maxPerBatch {
			batches = append(batches, makeDataBatchRequest(currentTag, version, auth))
			currentTag = &taggingpb.Tag{}
			currentCount = 0
		}

		currentTag.BlockNumber = append(currentTag.BlockNumber, bn)
		currentCount++
	}

	// Flush remaining
	if currentCount > 0 {
		batches = append(batches, makeDataBatchRequest(currentTag, version, auth))
	}

	return batches
}

// makeDataBatchRequest wraps a Tag into a DataSyncRequest with proper phase info.
func makeDataBatchRequest(tag *taggingpb.Tag, version uint16, auth *authpb.Auth) *datasyncpb.DataSyncRequest {
	return &datasyncpb.DataSyncRequest{
		Tag:     tag,
		Version: uint32(version),
		Ack: &ackpb.Ack{
			Ok:    true,
			Error: "",
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.DATA_SYNC_REQUEST,
			SuccessivePhase: constants.DATA_SYNC_RESPONSE,
			Success:         true,
			Error:           "",
			Auth:            auth,
		},
	}
}

func (ds *DataSync) Close() {
	// Note: Context cancellation should be handled by the caller who created the context
	// We only clean up our local references here
	if ds.SyncVars != nil {
		ds.SyncVars = nil
	}
	if ds.Comm != nil {
		ds.Comm = nil
	}
	
	ds.ServerAuth = nil
}
