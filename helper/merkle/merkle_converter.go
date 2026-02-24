package merkle

import (
	pbmerkle "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
)

// MerkleSnapshotToProto converts a domain MerkleTreeSnapshot to its Protobuf representation.
func MerkleSnapshotToProto(m *merkletree.MerkleTreeSnapshot) *pbmerkle.MerkleSnapshot {
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

func snapshotNodesToProto(nodes []*merkletree.SnapshotNode) []*pbmerkle.SnapshotNode {
	if nodes == nil {
		return nil
	}
	res := make([]*pbmerkle.SnapshotNode, len(nodes))
	for i, n := range nodes {
		// Nil peaks in the sparse accumulator array must be encoded as a sentinel
		// proto node (empty Root) so the slot index is preserved over the wire.
		if n == nil {
			res[i] = &pbmerkle.SnapshotNode{Root: []byte{}}
		} else {
			res[i] = snapshotNodeToProto(n)
		}
	}
	return res
}

func snapshotNodeToProto(n *merkletree.SnapshotNode) *pbmerkle.SnapshotNode {
	if n == nil {
		return &pbmerkle.SnapshotNode{Root: []byte{}} // sentinel: empty Root = nil slot
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
