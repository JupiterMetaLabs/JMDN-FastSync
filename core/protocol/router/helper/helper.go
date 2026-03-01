package helper

import (
	"errors"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
)

func GetLatestBlockNumber(b *merkletree.Builder) (uint64, error) {
	root, err := b.RootNode()
	if err != nil {
		return 0, err
	}
	if root == nil {
		return 0, errors.New("merkletree is empty")
	}

	// Traverse down to the rightmost leaf
	curr := root
	for curr.Right != nil {
		curr = curr.Right
	}

	// The latest block number is the end of the range of the rightmost node
	latestBlockNumber := curr.Metadata.Start + uint64(curr.Metadata.Count) - 1

	return latestBlockNumber, nil
}

func DivideTags(start uint64, end uint64) []*tagging.Tag {
	// Input is blocknumber range, output is tagging.Tag
	// Divide the range into chunks of 1500 blocks
	var tags []*tagging.Tag
	chunkSize := uint64(constants.MAX_HEADERS_PER_REQUEST)

	for i := start; i <= end; i += chunkSize {
		chunkEnd := i + chunkSize - 1
		if chunkEnd > end {
			chunkEnd = end
		}

		tags = append(tags, &tagging.Tag{
			Range: []*tagging.RangeTag{
				{
					Start: i,
					End:   chunkEnd,
				},
			},
		})
	}
	return tags
}
