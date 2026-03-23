package pots

import (
	"context"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// PoTS_router defines the interface for the Proof of Time Sync router,
// which manages synchronization variables, WAL setup, and PoTS lifecycle.
type PoTS_router interface {
	// SetSyncVars initializes synchronization variables including protocol version,
	// node information, and WAL reference, returning the router for chaining.
	SetSyncVars(ctx context.Context, protocolversion uint16, nodeInfo types.Nodeinfo) PoTS_router
	// StartPoTS begins the Proof of Time Sync process using the provided PoTS configuration.
	StartPoTS(ctx context.Context, pots *types.PoTS) PoTS_router
	// SetWAL initializes and returns a WAL interface for the given directory path.
	SetWAL(ctx context.Context, wal *WAL.WAL) PoTS_router
	// GetWAL return the PoTS_WAL Interface 
	GetWAL() (PoTS_WAL, error)
	// GetSyncVars returns the current synchronization variables.
	GetSyncVars() *types.Syncvars
	// GetPoTS returns the current Proof of Time Sync configuration.
	GetPoTS() *types.PoTS
	// Close shuts down the router and releases any held resources.
	Close()
}

// PoTS_WAL defines the interface for the Proof of Time Sync Write-Ahead Log,
// which handles persistent storage and retrieval of ZK blocks.
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