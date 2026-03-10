package router

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	merkle "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/merkle"
	router_helper "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/tagging"
	merkle_types "github.com/JupiterMetaLabs/JMDN-FastSync/helper/merkle"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/ion"

	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
)

const (
	// LEAF_THRESHOLD is the minimum number of blocks in a range before we stop bisecting
	// and just tag the entire range for synchronization.
	LEAF_THRESHOLD = 10

	// LAYER_THRESHOLD is the maximum recursion depth for bisection.
	// After this depth, we tag the entire remaining range to avoid excessive network calls.
	LAYER_THRESHOLD = 6

	// MAX_PARALLEL_REQUESTS limits concurrent network requests to avoid overwhelming the target node.
	MAX_PARALLEL_REQUESTS = 10
)

// bisectionWorkItem represents a range of blocks to be bisected at a specific layer.
type bisectionWorkItem struct {
	start uint64 // Starting block number
	count uint32 // Number of blocks in this range
	layer int    // Current layer/depth in the bisection tree
}

// rangeRequest represents a request for a sub-range merkle tree from the target node.
type rangeRequest struct {
	start      uint64
	count      uint32
	blockMerge int // BlockMerge to use for the sub-range tree
}

// rangeResponse contains the reconstructed Builder from the target node's snapshot.
type rangeResponse struct {
	start   uint64
	builder *merkletree.Builder
	err     error
}

// Inorder to get the merkle subtree from the client we need to register with it first.
// This funciton will register first before calling the merkle subtree request.
// can ignore this fields from the response - blockmerge, currentphase, multiaddr.
func (router *Datarouter) registerWithClient(ctx context.Context, remote *types.Nodeinfo) error {
	// Create availability request
	availabilityReq := &availabilitypb.AvailabilityRequest{
		Range: &merklepb.Range{
			Start: 0,
			End:   math.MaxUint64,
		},
	}

	// Send availability request to get UUID
	resp, err := router.Comm.SendAvailabilityRequest(ctx, *remote, availabilityReq)
	if err != nil {
		return fmt.Errorf("failed to send availability request: %w", err)
	}

	// Check if registration was successful
	if !resp.IsAvailable || resp.Auth == nil || resp.Auth.UUID == "" {
		return fmt.Errorf("registration failed: client not available or no UUID received")
	}

	// Store the generated UUID for future use
	router.clientGeneratedUUID = resp.Auth.UUID

	Log.Logger(namedlogger).Info(ctx, "Successfully registered with client",
		ion.String("clientUUID", resp.Auth.UUID),
		ion.String("remotePeerID", remote.PeerID.String()))

	return nil
}

// dataBisect performs breadth-first bisection using TreeDiff to find ALL differing
// ranges between local and target trees, then iteratively refines large ranges
// by requesting finer-grained sub-trees from the peer.
//
// Unlike the previous approach (TreeBisect which only returned the first mismatch),
// TreeDiff returns every differing range in a single pass, ensuring no mismatches
// are silently missed.
func (router *Datarouter) dataBisect(
	ctx context.Context,
	local_tree *merkletree.Builder,
	target_tree *merkletree.Builder,
	remote *types.Nodeinfo,
) (*headersyncpb.HeaderSyncRequest, error) {

	Log.Logger(namedlogger).Info(ctx, "Starting data bisection",
		ion.String("function", "dataBisect"))

	// Register with client first to get UUID for authentication
	if !router.IsRegistered() {
		Log.Logger(namedlogger).Info(ctx, "Registering with client for merkle subtree requests",
			ion.String("function", "dataBisect"))

		if err := router.registerWithClient(ctx, remote); err != nil {
			Log.Logger(namedlogger).Error(ctx, "Failed to register with client", err,
				ion.String("function", "dataBisect"))
			return nil, fmt.Errorf("failed to register with client: %w", err)
		}
	}

	tag := tagging.NewTagging()

	// Check if trees are already identical.
	root_local, err := local_tree.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to finalize local tree", err,
			ion.String("function", "dataBisect"))
		return nil, fmt.Errorf("failed to finalize local tree: %w", err)
	}

	root_target, err := target_tree.Finalize()
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to finalize target tree", err,
			ion.String("function", "dataBisect"))
		return nil, fmt.Errorf("failed to finalize target tree: %w", err)
	}

	if root_local == root_target {
		Log.Logger(namedlogger).Info(ctx, "Trees are identical, no bisection needed",
			ion.String("function", "dataBisect"))
		return &headersyncpb.HeaderSyncRequest{Tag: tag.Tag}, nil
	}

	// Use TreeDiff to find ALL differing ranges in one pass.
	// This is the critical fix: TreeBisect only returned the first (leftmost) mismatch,
	// silently missing all other differing ranges. TreeDiff traverses the entire tree
	// structure and returns every range where the two trees diverge.
	diffs, err := local_tree.TreeDiff(target_tree)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Initial TreeDiff failed", err,
			ion.String("function", "dataBisect"))
		return nil, fmt.Errorf("initial TreeDiff failed: %w", err)
	}

	if len(diffs) == 0 {
		Log.Logger(namedlogger).Warn(ctx, "TreeDiff returned no diffs despite different roots",
			ion.String("function", "dataBisect"))
		return &headersyncpb.HeaderSyncRequest{Tag: tag.Tag}, nil
	}

	Log.Logger(namedlogger).Info(ctx, "Initial diffs found",
		ion.Int("num_diffs", len(diffs)),
		ion.String("function", "dataBisect"))

	// Seed the work queue with all initial diffs.
	currentLayer := make([]bisectionWorkItem, 0, len(diffs))
	for _, d := range diffs {
		currentLayer = append(currentLayer, bisectionWorkItem{
			start: d.Start,
			count: d.Count,
			layer: 0,
		})
	}

	// Derive the peer's highest block number from the target tree's rightmost leaf.
	// Any diff range starting beyond this point means the peer doesn't have those blocks,
	// so there's no point requesting a sub-tree from them — tag immediately for sync.
	peerBlockHeight, err := router_helper.GetLatestBlockNumber(target_tree)
	if err != nil {
		Log.Logger(namedlogger).Warn(ctx, "Could not determine peer block height from target tree, skipping peer height check",
			ion.Err(err),
			ion.String("function", "dataBisect"))
		peerBlockHeight = ^uint64(0) // max uint64 — disables the check
	}

	// BREADTH-FIRST PROCESSING:
	// Process all ranges at the same layer before moving to the next layer.
	// At each layer, ranges that are small enough get tagged immediately.
	// Larger ranges are refined by requesting finer-grained sub-trees from the peer
	// and comparing them with TreeDiff again.
	for len(currentLayer) > 0 {
		if currentLayer[0].layer >= LAYER_THRESHOLD {
			Log.Logger(namedlogger).Info(ctx, "Reached layer threshold, tagging remaining ranges",
				ion.Int("layer", currentLayer[0].layer),
				ion.Int("remaining_ranges", len(currentLayer)),
				ion.String("function", "dataBisect"))
			break
		}

		Log.Logger(namedlogger).Debug(ctx, "Processing layer",
			ion.Int("layer", currentLayer[0].layer),
			ion.Int("num_ranges", len(currentLayer)),
			ion.String("function", "dataBisect"))

		nextLayer := []bisectionWorkItem{}
		itemsToProcess := []bisectionWorkItem{}

		for _, item := range currentLayer {
			if item.count <= LEAF_THRESHOLD {
				Log.Logger(namedlogger).Debug(ctx, "Range below leaf threshold, tagging",
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisect"))
				tag.TagRange(item.start, item.start+uint64(item.count)-1)
				continue
			}
			// If the range starts beyond the peer's last block, the peer cannot serve
			// a sub-tree for it. Tag directly — these are blocks the local node has
			// but the peer doesn't, so they need to be synced.
			if item.start > peerBlockHeight {
				Log.Logger(namedlogger).Debug(ctx, "Range beyond peer block height, tagging for sync",
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.Uint64("peer_block_height", peerBlockHeight),
					ion.String("function", "dataBisect"))
				tag.TagRange(item.start, item.start+uint64(item.count)-1)
				continue
			}
			itemsToProcess = append(itemsToProcess, item)
		}

		if len(itemsToProcess) == 0 {
			currentLayer = nextLayer
			continue
		}

		// Build batch requests. Each request uses a finer-grained BlockMerge so the
		// sub-tree has more leaf nodes, allowing deeper bisection.
		batchRequests := make([]rangeRequest, len(itemsToProcess))
		for i, item := range itemsToProcess {
			batchRequests[i] = rangeRequest{
				start:      item.start,
				count:      item.count,
				blockMerge: calculateSubBlockMerge(item.count),
			}
		}

		Log.Logger(namedlogger).Info(ctx, "Making batch network request for sub-trees",
			ion.Int("num_requests", len(batchRequests)),
			ion.Int("layer", itemsToProcess[0].layer),
			ion.String("function", "dataBisect"))

		// Parallel network calls: request sub-range merkle trees from the peer.
		batchResponses, err := router.requestSubTreesBatch(ctx, batchRequests, *remote)
		if err != nil {
			Log.Logger(namedlogger).Error(ctx, "Batch request failed", err,
				ion.String("function", "dataBisect"))
			return nil, fmt.Errorf("batch request failed at layer %d: %w", itemsToProcess[0].layer, err)
		}

		for i, item := range itemsToProcess {
			response := batchResponses[i]
			// WITH THIS:
			if response.err != nil || response.builder == nil {
				Log.Logger(namedlogger).Warn(ctx, "Sub-tree unavailable (peer missing or blocks not present), tagging range for sync",
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.Err(response.err),
					ion.String("function", "dataBisect"))
				tag.TagRange(item.start, item.start+uint64(item.count)-1)
				continue
			}

			target_subtree := response.builder

			// Build the local sub-tree with the SAME BlockMerge config so the tree
			// structures and hashes are directly comparable.
			local_subtree, err := router.buildLocalSubtree(ctx, item.start, item.count, batchRequests[i].blockMerge)
			if err != nil {
				Log.Logger(namedlogger).Error(ctx, "Failed to build local sub-tree", err,
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisect"))
				return nil, fmt.Errorf("failed to build local sub-tree for range [%d, %d]: %w",
					item.start, item.start+uint64(item.count)-1, err)
			}

			// Compare sub-trees using TreeDiff to find ALL sub-diffs.
			subDiffs, err := local_subtree.TreeDiff(target_subtree)
			if err != nil {
				Log.Logger(namedlogger).Error(ctx, "Sub-tree TreeDiff failed", err,
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisect"))
				return nil, fmt.Errorf("sub-tree TreeDiff failed for range [%d, %d]: %w",
					item.start, item.start+uint64(item.count)-1, err)
			}

			if len(subDiffs) == 0 {
				Log.Logger(namedlogger).Debug(ctx, "Sub-trees match, skipping",
					ion.Uint64("start", item.start),
					ion.Int("count", int(item.count)),
					ion.String("function", "dataBisect"))
				continue
			}

			for _, sd := range subDiffs {
				Log.Logger(namedlogger).Debug(ctx, "Found sub-diff, adding to next layer",
					ion.Uint64("sub_start", sd.Start),
					ion.Int("sub_count", int(sd.Count)),
					ion.Int("next_layer", item.layer+1),
					ion.String("function", "dataBisect"))

				nextLayer = append(nextLayer, bisectionWorkItem{
					start: sd.Start,
					count: sd.Count,
					layer: item.layer + 1,
				})
			}
		}

		currentLayer = nextLayer
	}

	// Tag any remaining items that hit the layer threshold.
	for _, item := range currentLayer {
		Log.Logger(namedlogger).Info(ctx, "Tagging range at layer threshold",
			ion.Uint64("start", item.start),
			ion.Int("count", int(item.count)),
			ion.Int("layer", item.layer),
			ion.String("function", "dataBisect"))
		tag.TagRange(item.start, item.start+uint64(item.count)-1)
	}

	Log.Logger(namedlogger).Info(ctx, "Bisection complete",
		ion.Int("num_block_tags", len(tag.Tag.BlockNumber)),
		ion.Int("num_range_tags", len(tag.Tag.Range)),
		ion.String("function", "dataBisect"))

	return &headersyncpb.HeaderSyncRequest{Tag: tag.Tag}, nil
}

// calculateSubBlockMerge determines the BlockMerge value for a sub-range tree.
// Uses 5% of the range size with a minimum of 1, ensuring the sub-tree is
// more granular than the parent tree.
func calculateSubBlockMerge(count uint32) int {
	bm := int(math.Ceil(float64(count) * 0.05))
	if bm < 1 {
		bm = 1
	}
	return bm
}

// requestSubTreesBatch makes parallel requests for sub-range merkle trees from
// the target node. Each response contains a reconstructed Builder ready for
// TreeDiff comparison.
func (router *Datarouter) requestSubTreesBatch(
	ctx context.Context,
	requests []rangeRequest,
	peerNode types.Nodeinfo,
) ([]rangeResponse, error) {
	if len(requests) == 0 {
		return []rangeResponse{}, nil
	}

	Log.Logger(namedlogger).Debug(ctx, "Starting batch sub-tree request",
		ion.Int("num_requests", len(requests)),
		ion.String("function", "requestSubTreesBatch"))

	responses := make([]rangeResponse, len(requests))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, MAX_PARALLEL_REQUESTS)

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, r rangeRequest) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			responses[idx] = router.requestSubTree(ctx, r, peerNode)
		}(i, req)
	}

	wg.Wait()

	Log.Logger(namedlogger).Debug(ctx, "Batch sub-tree request complete",
		ion.Int("num_responses", len(responses)),
		ion.String("function", "requestSubTreesBatch"))

	return responses, nil
}

// requestSubTree requests a merkle tree snapshot for a sub-range from the target node,
// reconstructs a Builder from the snapshot, and returns it for comparison.
//
// The request includes the desired SnapshotConfig (BlockMerge) so the target builds
// the sub-tree with the same granularity that the local node will use. This ensures
// the two trees are structurally compatible for TreeDiff comparison.
func (router *Datarouter) requestSubTree(
	ctx context.Context,
	req rangeRequest,
	peerNode types.Nodeinfo,
) rangeResponse {
	endInclusive := req.start + uint64(req.count) - 1

	Log.Logger(namedlogger).Debug(ctx, "Requesting sub-tree from target",
		ion.Uint64("start", req.start),
		ion.Uint64("end", endInclusive),
		ion.Int("blockMerge", req.blockMerge),
		ion.String("function", "requestSubTree"))

	// Include the Config in the request so the target builds the tree with the
	// same BlockMerge. Without this, the target would calculate its own BlockMerge
	// (5% of range) which could differ from what the local node uses, producing
	// incompatible tree structures and hashes.
	merkleReq := &merklepb.MerkleRequestMessage{
		Request: &merklepb.MerkleRequest{
			Start: req.start,
			End:   endInclusive,
			Config: &merklepb.SnapshotConfig{
				BlockMerge:    int32(req.blockMerge),
				ExpectedTotal: uint64(req.count),
			},
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.REQUEST_MERKLE,
			SuccessivePhase: constants.RESPONSE_MERKLE,
			Success:         true,
			Auth: &authpb.Auth{
				UUID: router.clientGeneratedUUID,
			},
		},
	}

	merkleResponse, err := router.Comm.SendMerkleRequest(ctx, peerNode, merkleReq)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to send Merkle Request", err,
			ion.String("function", "requestSubTree"))
		return rangeResponse{start: req.start, err: fmt.Errorf("SendMerkleRequest failed: %w", err)}
	}

	if merkleResponse.Ack != nil && !merkleResponse.Ack.Ok {
		err := fmt.Errorf("target node error: %s", merkleResponse.Ack.Error)
		Log.Logger(namedlogger).Error(ctx, "Target node returned error", err,
			ion.String("function", "requestSubTree"))
		return rangeResponse{start: req.start, err: err}
	}

	if merkleResponse.Snapshot == nil {
		err := fmt.Errorf("target returned nil snapshot for range [%d, %d]", req.start, endInclusive)
		Log.Logger(namedlogger).Error(ctx, "Target returned nil snapshot", err,
			ion.String("function", "requestSubTree"))
		return rangeResponse{start: req.start, err: err}
	}

	// Convert the protobuf snapshot to the domain type and reconstruct a live Builder.
	snap := merkle_types.ProtoToMerkleSnapshot(merkleResponse.Snapshot)
	if snap == nil {
		return rangeResponse{start: req.start, err: fmt.Errorf("failed to convert proto snapshot to domain type")}
	}

	builder, err := snap.FromSnapshot(nil)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Failed to reconstruct tree from snapshot", err,
			ion.String("function", "requestSubTree"))
		return rangeResponse{start: req.start, err: fmt.Errorf("failed to reconstruct tree from snapshot: %w", err)}
	}

	return rangeResponse{start: req.start, builder: builder}
}

// buildLocalSubtree generates a local merkle tree for a specific sub-range
// using the given BlockMerge config. This ensures the local and target sub-trees
// have identical structure for accurate TreeDiff comparison.
func (router *Datarouter) buildLocalSubtree(
	ctx context.Context,
	start uint64,
	count uint32,
	blockMerge int,
) (*merkletree.Builder, error) {
	if router.Nodeinfo == nil || router.Nodeinfo.BlockInfo == nil {
		return nil, fmt.Errorf("nodeinfo or blockinfo is nil")
	}

	merkle_obj := merkle.NewMerkleProof(router.Nodeinfo.BlockInfo)
	cfg := &merkletree.SnapshotConfig{
		BlockMerge:    blockMerge,
		ExpectedTotal: uint64(count),
	}

	endInclusive := start + uint64(count) - 1
	return merkle_obj.GenerateMerkleTreeWithConfig(ctx, start, endInclusive, cfg)
}
