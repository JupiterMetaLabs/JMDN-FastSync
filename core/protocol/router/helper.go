package router

import (
	"errors"

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
