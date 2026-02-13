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
// This is significantly more efficient than depth-first recursive bisection because:
// 1. All ranges at the same layer are requested in parallel (reduces latency)
// 2. Memory usage is bounded by the maximum number of ranges per layer
// 3. Total time is ~LAYER_THRESHOLD * network_latency instead of num_mismatches * depth * latency
func (router *Datarouter) dataBisect(
	ctx context.Context,
	local_tree *merkletree.Builder,
	target_tree *merkletree.Builder,
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

		batchResponses, err := router.requestLeafRangesBatch(ctx, batchRequests)
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
			resp, err := router.requestSingleRange(ctx, r.start, r.count)

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

// requestSingleRange requests block hashes for a specific range from the target node.
// TODO: This needs to be implemented with actual network protocol.
// For now, it's a placeholder that needs integration with your network layer.
func (router *Datarouter) requestSingleRange(
	ctx context.Context,
	start uint64,
	count uint32,
) (rangeResponse, error) {

	Log.Logger(namedlogger).Debug(ctx, "Requesting range from target node",
		ion.Uint64("start", start),
		ion.Int("count", int(count)),
		ion.String("function", "requestSingleRange"))

	// TODO: Implement actual network request here
	// This should:
	// 1. Send a REQUEST_LEAF_RANGE message to the target node
	// 2. Include the start block number and count
	// 3. Receive the response containing block hashes
	// 4. Return the hashes as []merkletree.Hash32

	// Placeholder implementation:
	// return rangeResponse{
	// 	start:  start,
	// 	hashes: receivedHashesFromNetwork,
	// 	err:    nil,
	// }, nil

	return rangeResponse{
		start:  start,
		hashes: nil,
		err:    fmt.Errorf("requestSingleRange not yet implemented - network protocol integration required"),
	}, fmt.Errorf("requestSingleRange not yet implemented")
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
		ion.Int("count", int(count)),
		ion.String("function", "getLocalHashes"))

	// Get block info interface
	blockInfo := router.Nodeinfo.BlockInfo
	if blockInfo == nil {
		return nil, fmt.Errorf("blockInfo is nil")
	}

	// TODO: Implement hash retrieval from local blockchain
	// This should:
	// 1. Iterate from start to start+count
	// 2. For each block, get its hash
	// 3. Return as []merkletree.Hash32

	// Placeholder implementation:
	hashes := make([]merkletree.Hash32, count)
	for i := uint32(0); i < count; i++ {
		blockNum := start + uint64(i)
		fmt.Print(blockNum)

		// TODO: Get actual block hash from blockchain
		// block := blockInfo.GetBlock(blockNum)
		// hashes[i] = block.Hash()

		// Log.Logger(namedlogger).Trace(ctx, "Getting hash for block",
		// 	ion.Uint64("block_number", blockNum),
		// 	ion.String("function", "getLocalHashes"))
	}

	return hashes, fmt.Errorf("getLocalHashes not yet implemented - blockchain interface integration required")
}
