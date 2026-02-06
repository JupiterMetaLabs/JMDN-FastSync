package merkle

import (
	"context"
	// "encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"

	"github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/JupiterMetaLabs/ion"
)

type MerkleProof struct {
	Blockinfo types.BlockInfo
}

type MerkleProofInterface interface {
	GenerateMerkleTree(logger_ctx context.Context, startBlock, endBlock int64) (*merkletree.MerkleTreeSnapshot, error)
	GenerateMerkleTreeWithConfig(logger_ctx context.Context, startBlock, endBlock int64, config *merkletree.SnapshotConfig) (*merkletree.MerkleTreeSnapshot, error)
	ReconstructTree(logger_ctx context.Context, snap *merkletree.MerkleTreeSnapshot) (*merkletree.Builder, error)
}

func NewMerkleProof() MerkleProofInterface {
	return &MerkleProof{}
}

func (m *MerkleProof) GenerateMerkleTree(logger_ctx context.Context, startBlock, endBlock int64) (*merkletree.MerkleTreeSnapshot, error) {

	if endBlock == -1 {
		// If the endBlock is -1, then we need to get the latest block number from the db.
		latestBlockNumber := m.Blockinfo.GetBlockNumber()

		// m.Blockinfo.GetBlockNumber().Client.Logger.Debug(logger_ctx, "Latest block number", ion.Int64("latest_block_number", int64(latestBlockNumber)), ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"))
		endBlock = int64(latestBlockNumber)
	} else if endBlock < startBlock && endBlock >= 0 {
		str := fmt.Sprintf("endBlock (%d) cannot be less than startBlock (%d)", endBlock, startBlock)
		err := errors.New(str)

		logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
			ion.Int64("start_block", startBlock),
			ion.Int64("end_block", endBlock),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)

		return nil, err
	} else if endBlock < -1 {
		str := fmt.Sprintf("endBlock (%d) cannot be less than -1", endBlock)
		err := errors.New(str)

		logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
			ion.Int64("start_block", startBlock),
			ion.Int64("end_block", endBlock),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)

		return nil, err
	}

	cfg := merkletree.Config{
		ExpectedTotal: uint64(endBlock - startBlock + 1),
		BlockMerge:    int(math.Ceil(float64(endBlock-startBlock+1) * 0.005)),
	}

	fmt.Println("BlockMerge: ", cfg.BlockMerge)

	Builder, err := merkletree.NewBuilder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Initialize the BlockIterator with a batch size (e.g., 1000)
	// We use 1000 to balance memory usage and network round trips.
	iterator := m.Blockinfo.NewBlockIterator(uint64(startBlock), uint64(endBlock), 1000)

	expectedBlockNumber := uint64(startBlock)

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
	if expectedBlockNumber <= uint64(endBlock) {
		gapSize := uint64(endBlock) - expectedBlockNumber + 1
		logging.Logger(logging.MerkleTree).Warn(logger_ctx, "Detected missing trailing blocks, filling with empty hashes",
			ion.Uint64("gap_start", expectedBlockNumber),
			ion.Uint64("gap_end", uint64(endBlock)),
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

	Builder.Visualize()

	// Use the library's ToSnapshot method
	snapshot := Builder.ToSnapshot()

	return snapshot, nil
}

func (m *MerkleProof) GenerateMerkleTreeWithConfig(logger_ctx context.Context, startBlock, endBlock int64, config *merkletree.SnapshotConfig) (*merkletree.MerkleTreeSnapshot, error) {

	if endBlock == -1 {
		// If the endBlock is -1, then we need to get the latest block number from the db.
		latestBlockNumber := m.Blockinfo.GetBlockNumber()

		// m.Blockinfo.GetBlockNumber().Client.Logger.Debug(logger_ctx, "Latest block number", ion.Int64("latest_block_number", int64(latestBlockNumber)), ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"))
		endBlock = int64(latestBlockNumber)
	} else if endBlock < startBlock && endBlock >= 0 {
		str := fmt.Sprintf("endBlock (%d) cannot be less than startBlock (%d)", endBlock, startBlock)
		err := errors.New(str)

		logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
			ion.Int64("start_block", startBlock),
			ion.Int64("end_block", endBlock),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)

		return nil, err
	} else if endBlock < -1 {
		str := fmt.Sprintf("endBlock (%d) cannot be less than -1", endBlock)
		err := errors.New(str)

		logging.Logger(logging.MerkleTree).Error(logger_ctx, "GenerateMerkleTree", err,
			ion.Int64("start_block", startBlock),
			ion.Int64("end_block", endBlock),
			ion.String("function", "DB_OPs.merkletree.GenerateMerkleTree"),
		)

		return nil, err
	}

	cfg := merkletree.Config{
		ExpectedTotal: uint64(endBlock - startBlock + 1),
		BlockMerge:    config.BlockMerge,
	}

	fmt.Println("BlockMerge: ", cfg.BlockMerge)

	Builder, err := merkletree.NewBuilder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Initialize the BlockIterator with a batch size (e.g., 1000)
	// We use 1000 to balance memory usage and network round trips.
	iterator := m.Blockinfo.NewBlockIterator(uint64(startBlock), uint64(endBlock), 1000)

	expectedBlockNumber := uint64(startBlock)

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
	if expectedBlockNumber <= uint64(endBlock) {
		gapSize := uint64(endBlock) - expectedBlockNumber + 1
		logging.Logger(logging.MerkleTree).Warn(logger_ctx, "Detected missing trailing blocks, filling with empty hashes",
			ion.Uint64("gap_start", expectedBlockNumber),
			ion.Uint64("gap_end", uint64(endBlock)),
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

	Builder.Visualize()

	// Use the library's ToSnapshot method
	snapshot := Builder.ToSnapshot()

	return snapshot, nil
}

// ReconstructTree restores a Merkle Builder from a MerkleTreeSnapshot.
func (m *MerkleProof) ReconstructTree(logger_ctx context.Context, snap *merkletree.MerkleTreeSnapshot) (*merkletree.Builder, error) {
	// Use the library's FromSnapshot method
	// We pass nil for HashFactory to use the default one
	builder, err := snap.FromSnapshot(nil)
	if err != nil {
		logging.Logger(logging.MerkleTree).Error(logger_ctx,  "Failed to restore builder from snapshot", err,
			ion.String("function", "DB_OPs.merkletree.ReconstructTree")		)
		return nil, fmt.Errorf("failed to restore builder from snapshot: %w", err)
	}

	logging.Logger(logging.MerkleTree).Info(logger_ctx, "Merkle Tree restored from snapshot",
		ion.String("function", "DB_OPs.merkletree.ReconstructTree"),
	)

	return builder, nil
}
