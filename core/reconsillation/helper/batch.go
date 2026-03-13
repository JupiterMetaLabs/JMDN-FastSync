package helper

import (
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
)

// Input take the map - split into batches of accounts for concurrent processing.
func SplitIntoBatches(accounts *taggingpb.TaggedAccounts, batchSize int) []map[string]bool {
	var batches []map[string]bool
	currentBatch := make(map[string]bool)

	for account := range accounts.Accounts {
		currentBatch[account] = true

		if len(currentBatch) >= batchSize {
			batches = append(batches, currentBatch)
			currentBatch = make(map[string]bool)
		}
	}

	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}

	return batches
}
