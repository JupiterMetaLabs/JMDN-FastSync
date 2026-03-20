package pots

import (
	"context"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

type PoTS_router interface {
	SetSyncVars(ctx context.Context, protocolversion uint16, nodeInfo types.Nodeinfo, wal *WAL.WAL) PoTS_router
	SetWAL(dir string) PoTS_WAL
	GetSyncVars() *types.Syncvars
	GetPoTS() *types.PoTS
	Close()
}

type PoTS_WAL interface {
	// Write writes a block to the WAL with context for cancellation
	Write(ctx context.Context, block *types.ZKBlock) error
	// WriteBatch writes multiple blocks to the WAL atomically
	WriteBatch(ctx context.Context, blocks []*types.ZKBlock) error
	// Read reads blocks from the WAL with pagination support
	Read(ctx context.Context, offset, limit int) ([]*types.ZKBlock, error)
	// ReadByRange reads blocks within a range of block numbers
	ReadByRange(ctx context.Context, startBlock, endBlock uint64) ([]*types.ZKBlock, error)
	// GetLatestBlockNumber returns the highest block number in WAL
	GetLatestBlockNumber(ctx context.Context) (uint64, error)
	// Count returns the total number of blocks in WAL
	Count(ctx context.Context) (uint64, error)
	// Close closes the WAL and releases resources
	Close() error
}