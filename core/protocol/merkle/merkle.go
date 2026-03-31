package merkle

import (
	"context"
	// "encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"

	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/JupiterMetaLabs/ion"
)

type MerkleProof struct {
	Blockinfo types.BlockInfo
}

type MerkleProofInterface interface {
	GenerateMerkleConfig(logger_ctx context.Context, startBlock, endBlock uint64) (merkletree.Config, error)
	GenerateMerkleTree(logger_ctx context.Context, startBlock, endBlock uint64) (*merkletree.Builder, error)
	GenerateMerkleTreeWithConfig(logger_ctx context.Context, startBlock, endBlock uint64, config *merkletree.SnapshotConfig) (*merkletree.Builder, error)
	ReconstructTree(logger_ctx context.Context, snap *merkletree.MerkleTreeSnapshot) (*merkletree.Builder, error)
	ToSnapshot(logger_ctx context.Context, builder *merkletree.Builder) (*merklepb.MerkleSnapshot, error)
}

func NewMerkleProof(blockInfo types.BlockInfo) MerkleProofInterface {
	return &MerkleProof{Blockinfo: blockInfo}
}

func (m *MerkleProof) GenerateMerkleConfig(logger_ctx context.Context, startBlock, endBlock uint64) (merkletree.Config, error) {

	cfg := merkletree.Config{
		ExpectedTotal: endBlock - startBlock + 1,
		BlockMerge:    int(math.Ceil(float64(endBlock-startBlock+1) * 0.005)),
	}

	return cfg, nil
}

func (m *MerkleProof) GenerateMerkleTree(logger_ctx context.Context, startBlock, endBlock uint64) (*merkletree.Builder, error) {

	if endBlock == math.MaxUint64 {
		// If the endBlock is -1, then we need to get the latest block number from the db.
		latestBlockNumber := m.Blockinfo.GetBlockNumber()

		// m.Blockinfo.GetBlockNumber().Client.Logger.Debug(logger_ctx, "Latest block number", ion.Int64("latest_block_number", int64(latestBlockNumber)), ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"))
		endBlock = latestBlockNumber
	} else if endBlock < startBlock {
		str := fmt.Sprintf("endBlock (%d) cannot be less than startBlock (%d)", endBlock, startBlock)
		err := errors.New(str)

		logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
			ion.Uint64("start_block", startBlock),
			ion.Uint64("end_block", endBlock),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)

		return nil, err
	}

	cfg := merkletree.Config{
		ExpectedTotal: endBlock - startBlock + 1,
		BlockMerge:    int(math.Ceil(float64(endBlock-startBlock+1) * 0.005)),
	}

	logging.Logger(logging.MerkleTree).Info(logger_ctx, "BlockMerge",
		ion.Int("block_merge", cfg.BlockMerge),
		ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"))

	Builder, err := merkletree.NewBuilder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Initialize the BlockIterator with a batch size (e.g., 1000)
	// We use 1000 to balance memory usage and network round trips.
	iterator := m.Blockinfo.NewBlockIterator(startBlock, endBlock, 1000)

	expectedBlockNumber := startBlock

	for {
		blocks, err := iterator.Next()
		if err != nil {
			logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
				ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
			)
			return nil, fmt.Errorf("failed to retrieve blocks: %w", err)
		}

		if blocks == nil {
			break
		}

		// Process each block individually to handle potential gaps within the batch
		for _, block := range blocks {
			// Check for gap before this block
			if block.BlockNumber > expectedBlockNumber {
				gapSize := block.BlockNumber - expectedBlockNumber
				logging.Logger(logging.MerkleTree).Warn(logger_ctx, "Detected missing blocks, filling with empty hashes",
					ion.Uint64("gap_start", expectedBlockNumber),
					ion.Uint64("gap_end", block.BlockNumber-1),
					ion.Uint64("gap_size", gapSize),
					ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
				)

				// Create padding for the gap
				padding := make([]merkletree.Hash32, gapSize)

				// Push padding to builder
				_, err = Builder.Push(expectedBlockNumber, padding)
				if err != nil {
					return nil, fmt.Errorf("failed to push padding for gap: %w", err)
				}

				expectedBlockNumber += gapSize
			}

			// Push this single block to builder
			hashe := merkletree.Hash32(block.BlockHash)
			// Push accepts a slice, so we wrap the single hash
			// Pass expectedBlockNumber (which should match block.BlockNumber if gap filled)
			_, err = Builder.Push(block.BlockNumber, []merkletree.Hash32{hashe})
			if err != nil {
				logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
					ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
				)
				return nil, fmt.Errorf("failed to push block %d: %w", block.BlockNumber, err)
			}

			// Update expected block number
			expectedBlockNumber = block.BlockNumber + 1
		}
	}

	// Check for trailing gap
	if expectedBlockNumber <= endBlock {
		gapSize := endBlock - expectedBlockNumber + 1
		logging.Logger(logging.MerkleTree).Warn(logger_ctx, "Detected missing trailing blocks, filling with empty hashes",
			ion.Uint64("gap_start", expectedBlockNumber),
			ion.Uint64("gap_end", endBlock),
			ion.Uint64("gap_size", gapSize),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)
		padding := make([]merkletree.Hash32, gapSize)
		_, err = Builder.Push(expectedBlockNumber, padding)
		if err != nil {
			return nil, fmt.Errorf("failed to push trailing padding: %w", err)
		}
	}

	_, err = Builder.Finalize()
	if err != nil {
		return nil, fmt.Errorf("failed to finalize merkle tree: %w", err)
	}

	if constants.DEVELOPMENT {
		Builder.Visualize()
	}

	return Builder, nil
}

func (m *MerkleProof) GenerateMerkleTreeWithConfig(logger_ctx context.Context, startBlock, endBlock uint64, config *merkletree.SnapshotConfig) (*merkletree.Builder, error) {

	if endBlock == math.MaxUint64 {
		// If the endBlock is MAX_UINT64, then we need to get the latest block number from the db.
		latestBlockNumber := m.Blockinfo.GetBlockNumber()

		// m.Blockinfo.GetBlockNumber().Client.Logger.Debug(logger_ctx, "Latest block number", ion.Int64("latest_block_number", int64(latestBlockNumber)), ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"))
		endBlock = latestBlockNumber
	} else if endBlock < startBlock {
		str := fmt.Sprintf("endBlock (%d) cannot be less than startBlock (%d)", endBlock, startBlock)
		err := errors.New(str)

		logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
			ion.Uint64("start_block", startBlock),
			ion.Uint64("end_block", endBlock),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)

		return nil, err
	}

	cfg := merkletree.Config{
		ExpectedTotal: endBlock - startBlock + 1,
		BlockMerge:    config.BlockMerge,
	}

	logging.Logger(logging.MerkleTree).Debug(logger_ctx, "BlockMerge",
		ion.Int("block_merge", cfg.BlockMerge),
		ion.String("function", "DB_OPs.merkletree.GenerateMerkleTreeWithConfig"))

	Builder, err := merkletree.NewBuilder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Initialize the BlockIterator with a batch size (e.g., 1000)
	// We use 1000 to balance memory usage and network round trips.
	iterator := m.Blockinfo.NewBlockIterator(startBlock, endBlock, 1000)

	expectedBlockNumber := startBlock

	for {
		blocks, err := iterator.Next()
		if err != nil {
			logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
				ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
			)
			return nil, fmt.Errorf("failed to retrieve blocks: %w", err)
		}

		if blocks == nil {
			break
		}

		// Process each block individually to handle potential gaps within the batch
		for _, block := range blocks {
			// Check for gap before this block
			if block.BlockNumber > expectedBlockNumber {
				gapSize := block.BlockNumber - expectedBlockNumber
				logging.Logger(logging.MerkleTree).Warn(logger_ctx, "Detected missing blocks, filling with empty hashes",
					ion.Uint64("gap_start", expectedBlockNumber),
					ion.Uint64("gap_end", block.BlockNumber-1),
					ion.Uint64("gap_size", gapSize),
					ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
				)

				// Create padding for the gap
				padding := make([]merkletree.Hash32, gapSize)

				// Push padding to builder
				_, err = Builder.Push(expectedBlockNumber, padding)
				if err != nil {
					return nil, fmt.Errorf("failed to push padding for gap: %w", err)
				}

				expectedBlockNumber += gapSize
			}

			// Push this single block to builder
			hashe := merkletree.Hash32(block.BlockHash)
			// Push accepts a slice, so we wrap the single hash
			// Pass expectedBlockNumber (which should match block.BlockNumber if gap filled)
			_, err = Builder.Push(block.BlockNumber, []merkletree.Hash32{hashe})
			if err != nil {
				logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
					ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
				)
				return nil, fmt.Errorf("failed to push block %d: %w", block.BlockNumber, err)
			}

			// Update expected block number
			expectedBlockNumber = block.BlockNumber + 1
		}
	}

	// Check for trailing gap
	if expectedBlockNumber <= endBlock {
		gapSize := endBlock - expectedBlockNumber + 1
		logging.Logger(logging.MerkleTree).Warn(logger_ctx, "Detected missing trailing blocks, filling with empty hashes",
			ion.Uint64("gap_start", expectedBlockNumber),
			ion.Uint64("gap_end", endBlock),
			ion.Uint64("gap_size", gapSize),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)
		padding := make([]merkletree.Hash32, gapSize)
		_, err = Builder.Push(expectedBlockNumber, padding)
		if err != nil {
			return nil, fmt.Errorf("failed to push trailing padding: %w", err)
		}
	}

	_, err = Builder.Finalize()
	if err != nil {
		return nil, fmt.Errorf("failed to finalize merkle tree: %w", err)
	}

	if constants.DEVELOPMENT {
		Builder.Visualize()
	}

	return Builder, nil
}

// ReconstructTree restores a Merkle Builder from a MerkleTreeSnapshot.
func (m *MerkleProof) ReconstructTree(logger_ctx context.Context, snap *merkletree.MerkleTreeSnapshot) (*merkletree.Builder, error) {

	// Use the library's FromSnapshot method
	// We pass nil for HashFactory to use the default one
	builder, err := snap.FromSnapshot(nil)
	if err != nil {
		logging.Logger(logging.MerkleTree).Error(logger_ctx, "Failed to restore builder from snapshot", err,
			ion.String("function", "DB_OPs.merkletree.ReconstructTree"))
		return nil, fmt.Errorf("failed to restore builder from snapshot: %w", err)
	}

	logging.Logger(logging.MerkleTree).Debug(logger_ctx, "Merkle Tree restored from snapshot",
		ion.String("function", "DB_OPs.merkletree.ReconstructTree"),
	)

	return builder, nil
}

func (m *MerkleProof) ToSnapshot(logger_ctx context.Context, builder *merkletree.Builder) (*merklepb.MerkleSnapshot, error) {
	// Use the library's ToSnapshot method
	snapshot := builder.ToSnapshot()

	// Build the snapshot peaks
	peaks := make([]*merklepb.SnapshotNode, len(snapshot.Peaks))
	for i, peak := range snapshot.Peaks {
		peaks[i] = convertSnapshotNode(peak)
	}

	merkle_snapshot := &merklepb.MerkleSnapshot{
		Version: int32(snapshot.Version),
		Config: &merklepb.SnapshotConfig{
			BlockMerge:    int32(snapshot.Config.BlockMerge),
			ExpectedTotal: uint64(snapshot.Config.ExpectedTotal),
		},
		TotalBlocks:        uint64(snapshot.TotalBlocks),
		ExpectedNextHeight: uint64(snapshot.ExpectedNextHeight),
		EnforceHeights:     snapshot.EnforceHeights,
		InChunkElems:       snapshot.InChunkElems,
		InChunkStart:       uint64(snapshot.InChunkStart),
		Peaks:              peaks,
	}

	return merkle_snapshot, nil
}

func convertSnapshotNode(node *merkletree.SnapshotNode) *merklepb.SnapshotNode {
	if node == nil {
		return nil
	}
	return &merklepb.SnapshotNode{
		Left:    convertSnapshotNode(node.Left),
		Right:   convertSnapshotNode(node.Right),
		Root:    node.Root,
		Start:   node.Start,
		Count:   node.Count,
		Data:    node.Data,
		HasData: node.HasData,
	}
}
