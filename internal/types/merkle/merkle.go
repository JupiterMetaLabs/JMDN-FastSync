package merkle

// MerkleTreeSnapshot is a plain struct designed for easy JSON/Protobuf serialization.
// It captures the complete state of a Builder.
type MerkleTreeSnapshot struct {
	Version int            `json:"version"`
	Config  SnapshotConfig `json:"config"`

	// State fields
	TotalBlocks        uint64 `json:"total_blocks"`
	ExpectedNextHeight uint64 `json:"expected_next_height"`
	EnforceHeights     bool   `json:"enforce_heights"`

	// Partial Chunk Buffer
	// InChunkElems contains the hashes of elements in the current partial chunk.
	// We use [][]byte because standard Go JSON marshals this as a list of base64 strings.
	InChunkElems [][]byte `json:"in_chunk_elems"`
	InChunkStart uint64   `json:"in_chunk_start"`

	// Outer Accumulator Peaks
	// Peaks are stored in order. Nil peaks in the sparse array are skipped or represented?
	// To be safe and explicit, let's store them as a list of *SnapshotNode,
	// preserving the index/level information is implied by structure or we can store sparse.
	// Current decision: Sparse array of peaks from the accumulator.
	Peaks []*SnapshotNode `json:"peaks"`
}

type SnapshotConfig struct {
	BlockMerge    int    `json:"block_merge"`
	ExpectedTotal uint64 `json:"expected_total"`
}

// SnapshotNode is a recursive struct for the Merkle Tree nodes.
type SnapshotNode struct {
	Left    *SnapshotNode `json:"left,omitempty"`
	Right   *SnapshotNode `json:"right,omitempty"`
	Root    []byte        `json:"root"` // serialized Hash32
	Start   uint64        `json:"start"`
	Count   uint32        `json:"count"`
	Data    []byte        `json:"data,omitempty"` // For leaves, this matches Root
	HasData bool          `json:"has_data"`
}