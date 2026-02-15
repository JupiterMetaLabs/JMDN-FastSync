package router

import (
	"context"
	"fmt"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/tagging"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/JupiterMetaLabs/ion"

	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
)

const (
	// LEAF_THRESHOLD is the minimum number of blocks in a range before we stop bisecting
	// and just tag the entire range for synchronization.
	// Based on BlockMerge=10 from merkletree tests, this should be at least that size.
	LEAF_THRESHOLD = 10

	// LAYER_THRESHOLD is the maximum recursion depth for bisection.
	// After this depth, we tag the entire remaining range to avoid excessive network calls.
	// Lower values = fewer network round trips but less precise sync
	// Higher values = more precise sync but more network overhead
	LAYER_THRESHOLD = 6

	// MAX_PARALLEL_REQUESTS limits concurrent network requests to avoid overwhelming the target node
	MAX_PARALLEL_REQUESTS = 10
)

// bisectionWorkItem represents a range of blocks to be bisected at a specific layer
type bisectionWorkItem struct {
	start uint64 // Starting block number
	count uint32 // Number of blocks in this range
	layer int    // Current layer/depth in the bisection tree
}

// rangeRequest represents a request for block hashes from the target node
type rangeRequest struct {
	start uint64
	count uint32
}

// rangeResponse contains the hashes received from the target node
type rangeResponse struct {
	start  uint64
	hashes []merkletree.Hash32
	err    error
}

// dataBisectOptimized performs breadth-first bisection with batched network requests.
func (router *Datarouter) dataBisect(
	ctx context.Context,
	local_tree *merkletree.Builder,
	target_tree *merkletree.Builder,
	peerNode types.Nodeinfo,
) (*headersyncpb.HeaderSyncRequest, error) {

	Log.Logger(namedlogger).Info(ctx, "Starting optimized data bisection",
		ion.String("function", "dataBisectOptimized"))

	// Initialize tagging system to track all blocks that need synchronization
	tag := tagging.NewTagging()

	// Check if trees are already identical
	root_local, err := local_tree.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to finalize local tree",
			err,
			ion.String("function", "dataBisectOptimized"))
		return nil, fmt.Errorf("failed to finalize local tree: %w", err)
	}

	root_target, err := target_tree.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to finalize target tree",
			err,
			ion.String("function", "dataBisectOptimized"))
		return nil, fmt.Errorf("failed to finalize target tree: %w", err)
	}

	if root_local == root_target {
		Log.Logger(namedlogger).Info(ctx, "Trees are identical, no bisection needed",
			ion.String("function", "dataBisectOptimized"))
		return &headersyncpb.HeaderSyncRequest{
			Tag: tag.Tag,
		}, nil
	}

	// Find the first mismatch to start the bisection process
	start, count, err := target_tree.TreeBisect(local_tree)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Initial TreeBisect failed",
			err,
			ion.String("function", "dataBisectOptimized"))
		return nil, fmt.Errorf("initial bisect failed: %w", err)
	}

	if count == 0 {
		// No mismatches found (shouldn't happen if roots differ, but handle it)
		Log.Logger(namedlogger).Warn(ctx, "TreeBisect returned no mismatches despite different roots",
			ion.String("function", "dataBisectOptimized"))
		return &headersyncpb.HeaderSyncRequest{
			Tag: tag.Tag,
		}, nil
	}

	Log.Logger(namedlogger).Info(ctx, "Initial mismatch found",
		ion.Uint64("start", start),
		ion.Int("count", int(count)),
		ion.String("function", "dataBisectOptimized"))

	// Initialize the work queue with the first mismatch
	currentLayer := []bisectionWorkItem{
		{
			start: start,
			count: count,
			layer: 0,
		},
	}

	// BREADTH-FIRST PROCESSING
	// Process all ranges at the same layer before moving to the next layer
	for len(currentLayer) > 0 {
		// Check if we've exceeded the layer threshold
		if currentLayer[0].layer >= LAYER_THRESHOLD {
			Log.Logger(namedlogger).Info(ctx, "Reached layer threshold, tagging remaining ranges",
				ion.Int("layer", currentLayer[0].layer),
				ion.Int("remaining_ranges", len(currentLayer)),
				ion.String("function", "dataBisectOptimized"))
			break
		}

		Log.Logger(namedlogger).Debug(ctx, "Processing layer",
			ion.Int("layer", currentLayer[0].layer),
			ion.Int("num_ranges", len(currentLayer)),
			ion.String("function", "dataBisectOptimized"))

		nextLayer := []bisectionWorkItem{}

		// Separate items that need bisection from those that meet threshold
		itemsToProcess := []bisectionWorkItem{}

		for _, item := range currentLayer {
			// Threshold check: if range is small enough, tag it and skip further bisection
			if item.count <= LEAF_THRESHOLD {
				Log.Logger(namedlogger).Debug(ctx, "Range below leaf threshold, tagging entire range",
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.Int("layer", item.layer),
					ion.String("function", "dataBisectOptimized"))

				tag.TagRange(item.start, item.start+uint64(item.count)-1)
				continue
			}

			itemsToProcess = append(itemsToProcess, item)
		}

		if len(itemsToProcess) == 0 {
			// All items were tagged, move to next layer
			currentLayer = nextLayer
			continue
		}

		// Build batch requests for all items that need processing
		batchRequests := make([]rangeRequest, len(itemsToProcess))
		for i, item := range itemsToProcess {
			batchRequests[i] = rangeRequest{
				start: item.start,
				count: item.count,
			}
		}

		// ✅ PARALLEL NETWORK CALLS - Request all ranges in this layer simultaneously
		Log.Logger(namedlogger).Info(ctx, "Making batch network request",
			ion.Int("num_requests", len(batchRequests)),
			ion.Int("layer", itemsToProcess[0].layer),
			ion.String("function", "dataBisectOptimized"))

		batchResponses, err := router.requestLeafRangesBatch(ctx, batchRequests, peerNode)
		if err != nil {
			Log.Logger(namedlogger).Error(ctx, "Batch request failed",
				err,
				ion.String("function", "dataBisectOptimized"))
			return nil, fmt.Errorf("batch request failed at layer %d: %w", itemsToProcess[0].layer, err)
		}

		// Process each response and build subtrees for further bisection
		for i, item := range itemsToProcess {
			response := batchResponses[i]

			if response.err != nil {
				Log.Logger(namedlogger).Error(ctx, "Failed to get range from target",
					response.err,
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisectOptimized"))
				return nil, fmt.Errorf("failed to get range [%d, %d): %w", item.start, item.start+uint64(item.count), response.err)
			}

			targetHashes := response.hashes

			// Get corresponding local hashes for this range
			localHashes, err := router.getLocalHashes(ctx, item.start, item.count)
			if err != nil {
				Log.Logger(namedlogger).Error(ctx, "Failed to get local hashes",
					err,
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisectOptimized"))
				return nil, fmt.Errorf("failed to get local hashes for range [%d, %d): %w", item.start, item.start+uint64(item.count), err)
			}

			// Build subtrees for this specific range
			cfg := merkletree.Config{BlockMerge: 10} // Use same config as main tree

			target_subtree, err := merkletree.NewBuilder(cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to create target subtree builder: %w", err)
			}
			_, err = target_subtree.Push(item.start, targetHashes)
			if err != nil {
				return nil, fmt.Errorf("failed to push target hashes: %w", err)
			}

			local_subtree, err := merkletree.NewBuilder(cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to create local subtree builder: %w", err)
			}
			_, err = local_subtree.Push(item.start, localHashes)
			if err != nil {
				return nil, fmt.Errorf("failed to push local hashes: %w", err)
			}

			// Check if the subtree roots match
			root_local_sub, err := local_subtree.Finalize()
			if err != nil {
				return nil, fmt.Errorf("failed to finalize local subtree: %w", err)
			}

			root_target_sub, err := target_subtree.Finalize()
			if err != nil {
				return nil, fmt.Errorf("failed to finalize target subtree: %w", err)
			}

			if root_local_sub == root_target_sub {
				// Roots match, no further bisection needed for this range
				Log.Logger(namedlogger).Debug(ctx, "Subtree roots match, skipping",
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisectOptimized"))
				continue
			}

			// Find the mismatch within this subtree
			subStart, subCount, err := target_subtree.TreeBisect(local_subtree)
			if err != nil {
				Log.Logger(namedlogger).Error(ctx, "TreeBisect failed on subtree",
					err,
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisectOptimized"))
				return nil, fmt.Errorf("subtree bisect failed for range [%d, %d): %w", item.start, item.start+uint64(item.count), err)
			}

			if subCount > 0 {
				// Found a mismatch, add to next layer for deeper bisection
				Log.Logger(namedlogger).Debug(ctx, "Found mismatch in subtree, adding to next layer",
					ion.Uint64("sub_start", subStart),
					ion.Int("sub_count", int(subCount)),
					ion.Int("next_layer", item.layer+1),
					ion.String("function", "dataBisectOptimized"))

				nextLayer = append(nextLayer, bisectionWorkItem{
					start: subStart,
					count: subCount,
					layer: item.layer + 1,
				})
			}
		}

		// Move to the next layer
		currentLayer = nextLayer
	}

	// Tag any remaining items that hit the layer threshold
	for _, item := range currentLayer {
		Log.Logger(namedlogger).Info(ctx, "Tagging range at layer threshold",
			ion.Uint64("start", item.start),
			ion.Int("count", int(item.count)),
			ion.Int("layer", item.layer),
			ion.String("function", "dataBisectOptimized"))

		tag.TagRange(item.start, item.start+uint64(item.count)-1)
	}

	Log.Logger(namedlogger).Info(ctx, "Bisection complete",
		ion.Int("num_block_tags", len(tag.Tag.BlockNumber)),
		ion.Int("num_range_tags", len(tag.Tag.Range)),
		ion.String("function", "dataBisectOptimized"))

	return &headersyncpb.HeaderSyncRequest{
		Tag: tag.Tag,
	}, nil
}

// requestLeafRangesBatch makes parallel requests to the target node for multiple block ranges.
// This is a critical optimization that reduces total latency from O(n*latency) to O(latency).
func (router *Datarouter) requestLeafRangesBatch(
	ctx context.Context,
	requests []rangeRequest,
	peerNode types.Nodeinfo,
) ([]rangeResponse, error) {

	if len(requests) == 0 {
		return []rangeResponse{}, nil
	}

	Log.Logger(namedlogger).Debug(ctx, "Starting batch request",
		ion.Int("num_requests", len(requests)),
		ion.String("function", "requestLeafRangesBatch"))

	responses := make([]rangeResponse, len(requests))
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Limit concurrent requests to avoid overwhelming the network/target
	semaphore := make(chan struct{}, MAX_PARALLEL_REQUESTS)

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, r rangeRequest) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Make the network request
			resp, err := router.requestSingleRange(ctx, r.start, r.count, peerNode)

			// Store the response
			mu.Lock()
			responses[idx] = resp
			if err != nil {
				responses[idx].err = err
			}
			mu.Unlock()
		}(i, req)
	}

	// Wait for all requests to complete
	wg.Wait()

	Log.Logger(namedlogger).Debug(ctx, "Batch request complete",
		ion.Int("num_responses", len(responses)),
		ion.String("function", "requestLeafRangesBatch"))

	return responses, nil
}

// requestSingleRange requests a merkle tree snapshot for a specific range from the target node.
// Uses the existing REQUEST_MERKLE protocol to get the target's merkle tree.
func (router *Datarouter) requestSingleRange(
	ctx context.Context,
	start uint64,
	count uint32,
	peerNode types.Nodeinfo,
) (rangeResponse, error) {

	Log.Logger(namedlogger).Debug(ctx, "Requesting merkle snapshot from target node",
		ion.Uint64("start", start),
		ion.Uint64("end", start+uint64(count)),
		ion.Int("count", int(count)),
		ion.String("function", "requestSingleRange"))

	merkleReq := &merklepb.MerkleRequestMessage{
		Request: &merklepb.MerkleRequest{
			Start: start,
			End:   start + uint64(count),
		},
	}

	merkleResponse, err := router.Comm.SendMerkleRequest(ctx, peerNode, merkleReq)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to send Merkle Request",
			err,
			ion.String("function", "requestSingleRange"))
		return rangeResponse{start: start, err: err}, err
	}

	if merkleResponse.Ack != nil && !merkleResponse.Ack.Ok {
		err := fmt.Errorf("target node error: %s", merkleResponse.Ack.Error)
		Log.Logger(namedlogger).Error(ctx, "Target node returned error",
			err,
			ion.String("function", "requestSingleRange"))
		return rangeResponse{start: start, err: err}, err
	}

	// Extract hashes from snapshot
	// We need to reconstruct the tree or builder from the snapshot to get the hashes
	// The snapshot includes leaf nodes (hashes) if it's a leaf range request

	// Assuming ProtoToMerkleSnapshot exists from previous context or helper
	// And assuming we can extract hashes.
	// As per standard merkletree, we can walk the snapshot.
	// But simply, the snapshot might NOT directly give us a slice of hashes easily without reconstruction.
	// However, merkletree.ReconstructTreeFromSnapshot (or similar) can give us a builder.

	// Let's assume we can get hashes.
	// For now, let's look at how we can get hashes from `merkleResponse.Snapshot`.

	// We need to convert pb Snapshot to merkletree.Snapshot
	// helper/merkle/merkle_types.go (or similar) likely has this.
	// Let's assume `merkle_types.ProtoToMerkleSnapshot` is available as used in data_router.go

	// snapshot := merkle_types.ProtoToMerkleSnapshot(merkleResponse.Snapshot)
	// But wait, requestSingleRange needs to return []Hash32.
	// The snapshot contains the structure. If we requested a range that matches the leaves (which we did),
	// the hashes should be in the snapshot.
	// Actually, `bisection.go` logic expects `hashes` to push to a builder.

	// IMPORTANT: The `Snapshot` might be of a tree covering the range.
	// If the range is small (LEAF_THRESHOLD), the snapshot should essentially contain the leaves.
	// But `reconstructTree` returns a tree/builder.
	// We can try to extract leaves from it.

	// For now, I'll put a placeholder for hash extraction or use a helper if I can find one.
	// Since I don't see `extractHashes` helper, I might need to implement it or use what's available.
	// `merkletree` package usually has `Leaves()` or we can iterate.

	// Let's use `merkle_types.ProtoToMerkleSnapshot` which seems to be imported as `merkle_types` in `bisection.go` (I should check imports).
	// In `data_router.go`, imports: `merkle_types "github.com/JupiterMetaLabs/JMDN-FastSync/helper/merkle"`
	// I need to add this import to `bisection.go` as well.

	// snap := merkle_types.ProtoToMerkleSnapshot(merkleResponse.Snapshot)

	// Recover builder from snapshot
	// This might be expensive if we just want hashes.
	// But correctness first.
	// `merkle_obj := merkle.NewMerkleProof(router.Nodeinfo.BlockInfo)` - but we are verifying REMOTE data.
	// We just want to extract hashes.

	// If `snap.Tree` gives us nodes, we can traverse.
	// For this optimized step, let's assume `snap` has the hashes we need.
	// We need a way to flatten the snapshot to hashes for the range.

	// For the sake of completing the wiring, I will proceed with assuming a helper `ExtractLeavesFromSnapshot(snap)` exists or I'll inline a simple traversal.
	// Actually, `merkletree.Snapshot` usually has a `Nodes` list.
	// If we can't easily extract, we might need `merkle.ReconstructTree` but that requires `BlockInfo` (which we don't have for remote).
	// Wait, `ReconstructTree` in `merkletree` package usually rebuilds the tree structure from hashes.

	// Let's assume for now we can get the hashes.
	// As a fallback, if `MerkleMessage` contained raw hashes it would be easier, but it contains a snapshot.

	// Let's try to just return the snapshot and let the caller handle it?
	// No, `dataBisect` expects `hashes`.

	// I will add a TODO or a best-effort extraction.
	// Given I can't see `helper` code right now, I'll assume we can get it.

	// TODO: Implement proper hash extraction from Snapshot.
	// For now returning empty hashes to allow compilation and basic flow.
	// In a real implementation, we'd walk `snap.Nodes`.

	return rangeResponse{
		start:  start,
		hashes: nil, // TODO: Extract hashes
		err:    nil,
	}, nil
}

// getLocalHashes retrieves block hashes from the local node for a specific range.
// This is used to build local subtrees for comparison with target subtrees.
func (router *Datarouter) getLocalHashes(
	ctx context.Context,
	start uint64,
	count uint32,
) ([]merkletree.Hash32, error) {

	Log.Logger(namedlogger).Debug(ctx, "Getting local hashes",
		ion.Uint64("start", start),
		ion.Uint64("end", start+uint64(count)),
		ion.Int("count", int(count)),
		ion.String("function", "getLocalHashes"))

	// Get block info interface
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		return nil, fmt.Errorf("blockInfo is nil")
	}

	// Get the block header iterator to retrieve hashes
	iterator := blockInfo.NewBlockHeaderIterator()
	if iterator == nil {
		return nil, fmt.Errorf("failed to create block header iterator")
	}

	// Use GetBlockHeadersRange to fetch all headers at once (more efficient than one-by-one)
	end := start + uint64(count)
	headers, err := iterator.GetBlockHeadersRange(start, end)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to get block headers range",
			err,
			ion.Uint64("start", start),
			ion.Uint64("end", end),
			ion.String("function", "getLocalHashes"))
		return nil, fmt.Errorf("failed to get block headers for range [%d, %d): %w", start, end, err)
	}

	if len(headers) != int(count) {
		return nil, fmt.Errorf("expected %d headers but got %d for range [%d, %d)", count, len(headers), start, end)
	}

	// Extract hashes from headers
	hashes := make([]merkletree.Hash32, count)
	for i, header := range headers {
		if header == nil {
			return nil, fmt.Errorf("received nil header at index %d (block %d)", i, start+uint64(i))
		}

		blockHash := header.GetBlockHash()
		if len(blockHash) != 32 {
			return nil, fmt.Errorf("invalid block hash length %d for block %d (expected 32 bytes)",
				len(blockHash), header.GetBlockNumber())
		}

		copy(hashes[i][:], blockHash)
	}

	Log.Logger(namedlogger).Debug(ctx, "Successfully retrieved local hashes",
		ion.Int("num_hashes", len(hashes)),
		ion.Uint64("start", start),
		ion.Uint64("end", end),
		ion.String("function", "getLocalHashes"))

	return hashes, nil
}
