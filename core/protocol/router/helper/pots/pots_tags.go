package potshelper

import (
	"fmt"
	"slices"

	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	tagpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/tagging"
)

/*
 Take input as the potspb.PoTSRequest
 1. Take the latest blocknumber, query the block headers from the Client's latestblocknumber to current latestblocknumber from the localdb using interfaces from NodeInfo
 2. Generate the Hashmap of blocknumber to blockhash
 3. Compare the hashmap with the client's hashmap using CompareMaps function in local package.
 4. Generate the tags based on the map returned by the CompareMaps function.
 5. make it as the response using the NewPoTSResponseBuilder function and return back to the caller function.
 Overall Time Complexity = $O(N) + O(N) + O(N) + O(N) + O(1)$ → $O(N)$ linear time.
*/

type PoTSHelper struct {
	Nodeinfo *types.Nodeinfo
}

// NewPoTSHelper creates a new PoTSHelper instance
func NewPoTSHelper(nodeinfo *types.Nodeinfo) *PoTSHelper {
	return &PoTSHelper{
		Nodeinfo: nodeinfo,
	}
}

// ProcessPoTSRequest processes the PoTS request and generates the response
// Time Complexity: O(N) where N is the number of blocks to sync
func (ph *PoTSHelper) ProcessPoTSRequest(request *potspb.PoTSRequest) (*potspb.PoTSResponse, error) {
	if request == nil {
		return NewPoTSResponseBuilder().
			AddStatus(false).
			AddErrorMessage("request is nil").
			AddSuccesivePhase(constants.FAILURE).
			Build(), fmt.Errorf("request is nil")
	}

	if ph.Nodeinfo == nil || ph.Nodeinfo.BlockInfo == nil {
		return NewPoTSResponseBuilder().
			AddStatus(false).
			AddErrorMessage("nodeinfo or blockinfo not initialized").
			AddSuccesivePhase(constants.FAILURE).
			Build(), fmt.Errorf("nodeinfo not initialized")
	}

	// 1. Get the server's latest block number
	serverLatestBlock := ph.Nodeinfo.BlockInfo.GetBlockNumber()

	// Get client's latest block number from request
	clientLatestBlock := request.LatestBlockNumber

	// If client is already up-to-date or ahead
	if clientLatestBlock >= serverLatestBlock {
		return NewPoTSResponseBuilder().
			AddStatus(true).
			AddLatestBlockNumber(serverLatestBlock).
			AddTag(&tagpb.Tag{}). // Empty tag - nothing to sync
			AddSuccesivePhase(constants.HEADER_SYNC_REQUEST).
			Build(), nil
	}

	// 2. Query block headers from client's latest to server's latest
	// Time complexity: O(N) where N = serverLatestBlock - clientLatestBlock
	headers, err := ph.Nodeinfo.BlockInfo.NewBlockHeaderIterator().GetBlockHeadersRange(
		clientLatestBlock+1,
		serverLatestBlock,
	)
	if err != nil {
		return NewPoTSResponseBuilder().
			AddStatus(false).
			AddErrorMessage(fmt.Sprintf("failed to query block headers: %v", err)).
			AddLatestBlockNumber(serverLatestBlock).
			AddSuccesivePhase(constants.FAILURE).
			Build(), fmt.Errorf("failed to query headers: %w", err)
	}

	// Safety check: ensure we got headers when we expected them
	if len(headers) == 0 {
		return NewPoTSResponseBuilder().
			AddStatus(false).
			AddErrorMessage(fmt.Sprintf("no headers found in range %d-%d", clientLatestBlock+1, serverLatestBlock)).
			AddLatestBlockNumber(serverLatestBlock).
			AddSuccesivePhase(constants.FAILURE).
			Build(), fmt.Errorf("no headers returned for range %d-%d", clientLatestBlock+1, serverLatestBlock)
	}

	// 3. Generate hashmap of block_number -> block_hash from server's DB
	// Time complexity: O(N)
	serverMap := make(map[uint64][]byte, len(headers))
	for _, header := range headers {
		serverMap[header.BlockNumber] = header.BlockHash
	}

	// 4. Compare with client's hashmap
	// Time complexity: O(N)
	diffMap := CompareMaps(serverMap, request.Blocks)

	// 5. Generate tags from the difference map
	// Time complexity: O(N)
	tags := GenerateTagsFromMap(diffMap)

	// 6. Build and return response
	// Time complexity: O(1)
	return NewPoTSResponseBuilder().
		AddStatus(true).
		AddTag(tags).
		AddLatestBlockNumber(serverLatestBlock).
		AddSuccesivePhase(constants.HEADER_SYNC_REQUEST).
		Build(), nil
}

// GenerateTagsFromMap converts a map of block numbers to a Tag with optimized ranges
// Time Complexity: O(N log N) due to sorting, then O(N) for range detection = O(N log N)
// However, if we receive the map already sorted, we can optimize to O(N)
func GenerateTagsFromMap(blockMap map[uint64][]byte) *tagpb.Tag {
	if len(blockMap) == 0 {
		return &tagpb.Tag{}
	}

	// Extract block numbers and sort them
	// Time complexity: O(N)
	blockNumbers := make([]uint64, 0, len(blockMap))
	for blockNum := range blockMap {
		blockNumbers = append(blockNumbers, blockNum)
	}

	// Sort block numbers
	// Time complexity: O(N log N)
	slices.Sort(blockNumbers)

	// Create tagging instance
	tagger := tagging.NewTagging()

	// Generate ranges and individual blocks
	// Time complexity: O(N)
	i := 0
	for i < len(blockNumbers) {
		start := blockNumbers[i]
		end := start

		// Find consecutive blocks to form a range
		for i+1 < len(blockNumbers) && blockNumbers[i+1] == blockNumbers[i]+1 {
			i++
			end = blockNumbers[i]
		}

		// If we have a range (at least 2 consecutive blocks), create a RangeTag
		// Otherwise, create individual block tag
		if end > start {
			tagger.TagRange(start, end)
		} else {
			tagger.TagBlock(start)
		}

		i++
	}

	return tagger.Tag
}

