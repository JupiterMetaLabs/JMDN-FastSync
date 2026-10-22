package merkle

import (
	pbmerkle "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
)

func ProtoToMerkleSnapshot(m *pbmerkle.MerkleSnapshot) *merkletree.MerkleTreeSnapshot {
	if m == nil {
		return nil
	}

	return &merkletree.MerkleTreeSnapshot{
		Version: int(m.Version),
		Config: merkletree.SnapshotConfig{
			BlockMerge:    int(m.Config.BlockMerge),
			ExpectedTotal: m.Config.ExpectedTotal,
		},
		TotalBlocks:        m.TotalBlocks,
		ExpectedNextHeight: m.ExpectedNextHeight,
		EnforceHeights:     m.EnforceHeights,
		InChunkElems:       m.InChunkElems,
		InChunkStart:       m.InChunkStart,
		Peaks:              protoToSnapshotNodes(m.Peaks),
	}
}

func protoToSnapshotNodes(nodes []*pbmerkle.SnapshotNode) []*merkletree.SnapshotNode {
	if nodes == nil {
		return nil
	}
	res := make([]*merkletree.SnapshotNode, len(nodes))
	for i, n := range nodes {
		res[i] = protoToSnapshotNode(n)
	}
	return res
}

func protoToSnapshotNode(n *pbmerkle.SnapshotNode) *merkletree.SnapshotNode {
	// A nil proto node OR a sentinel node (empty/nil Root) represents an empty
	// slot in the sparse peaks accumulator — restore it as nil.
	if n == nil || len(n.Root) != 32 {
		return nil
	}
	return &merkletree.SnapshotNode{
		Left:    protoToSnapshotNode(n.Left),
		Right:   protoToSnapshotNode(n.Right),
		Root:    n.Root,
		Start:   n.Start,
		Count:   n.Count,
		Data:    n.Data,
		HasData: n.HasData,
	}
}
