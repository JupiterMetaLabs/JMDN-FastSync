package merkle

import (
	"testing"

	pbmerkle "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
)

// A peer can omit the config field; conversion must not panic.
func TestProtoToMerkleSnapshotWithoutConfig(t *testing.T) {
	snap := ProtoToMerkleSnapshot(&pbmerkle.MerkleSnapshot{TotalBlocks: 5})
	if snap == nil || snap.TotalBlocks != 5 || snap.Config.BlockMerge != 0 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}
