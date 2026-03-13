package test

import (
	"testing"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/reconsillation/helper"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
)

func TestSplitIntoBatches(t *testing.T) {
	accounts := &taggingpb.TaggedAccounts{
		Accounts: map[string]bool{
			"account1": true,
			"account2": true,
			"account3": true,
			"account4": true,
			"account5": true,
		},
	}

	batches := helper.SplitIntoBatches(accounts, 2)

	if len(batches) != 3 {
		t.Errorf("Expected 3 batches, got %d", len(batches))
	}

	if len(batches[0]) != 2 {
		t.Errorf("Expected 2 accounts in first batch, got %d", len(batches[0]))
	}

	if len(batches[1]) != 2 {
		t.Errorf("Expected 2 accounts in second batch, got %d", len(batches[1]))
	}

	if len(batches[2]) != 1 {
		t.Errorf("Expected 1 account in third batch, got %d", len(batches[2]))
	}
}
