package merkle

import (
	pbmerkle "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
)

// MerkleSnapshotToProto converts a domain MerkleTreeSnapshot to its Protobuf representation.
func MerkleSnapshotToProto(m *MerkleTreeSnapshot) *pbmerkle.MerkleSnapshot {
	if m == nil {
		return nil
	}

	return &pbmerkle.MerkleSnapshot{
		Version: int32(m.Version),
		Config: &pbmerkle.SnapshotConfig{
			BlockMerge:    int32(m.Config.BlockMerge),
			ExpectedTotal: m.Config.ExpectedTotal,
		},
		TotalBlocks:        m.TotalBlocks,
		ExpectedNextHeight: m.ExpectedNextHeight,
		EnforceHeights:     m.EnforceHeights,
		InChunkElems:       m.InChunkElems,
		InChunkStart:       m.InChunkStart,
		Peaks:              snapshotNodesToProto(m.Peaks),
	}
}

func snapshotNodesToProto(nodes []*SnapshotNode) []*pbmerkle.SnapshotNode {
	if nodes == nil {
		return nil
	}
	res := make([]*pbmerkle.SnapshotNode, len(nodes))
	for i, n := range nodes {
		res[i] = snapshotNodeToProto(n)
	}
	return res
}

func snapshotNodeToProto(n *SnapshotNode) *pbmerkle.SnapshotNode {
	if n == nil {
		return nil
	}
	return &pbmerkle.SnapshotNode{
		Left:    snapshotNodeToProto(n.Left),
		Right:   snapshotNodeToProto(n.Right),
		Root:    n.Root,
		Start:   n.Start,
		Count:   n.Count,
		Data:    n.Data,
		HasData: n.HasData,
	}
}
