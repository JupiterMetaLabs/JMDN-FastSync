package pots

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
)

// PoTSWAL is the write-ahead log for blocks collected during the PoTS window.
//
// Storage layout:
//   - Each ZKBlock is JSON-encoded and stored as a PoTSBlockEvent in the underlying WAL.
//   - An in-memory blockIndex (blockNumber → LSN) is maintained for O(1) existence
//     checks during Phase 2 deduplication, and rebuilt from the WAL on startup.
//   - lsnOrder preserves insertion order so Read(offset, limit) can page correctly.
type PoTSWAL struct {
	wal *WAL.WAL

	mu          sync.RWMutex
	blockIndex  map[uint64]uint64 // blockNumber → LSN
	lsnOrder    []uint64          // insertion-ordered LSNs for pagination
	latestBlock uint64
}

// NewPoTSWAL creates a PoTSWAL wrapper around the provided WAL instance and
// replays existing entries to rebuild the in-memory index. Returns a ready-to-use PoTSWAL.
func NewPoTSWAL(ctx context.Context, wal *WAL.WAL) (*PoTSWAL, error) {
	pw := &PoTSWAL{
		wal:        wal,
		blockIndex: make(map[uint64]uint64),
	}

	// Rebuild in-memory index from any existing WAL entries (crash recovery).
	if err := pw.rebuildIndex(); err != nil {
		return nil, fmt.Errorf("pots WAL index rebuild failed: %w", err)
	}

	return pw, nil
}

// rebuildIndex replays all PoTS WAL entries and reconstructs blockIndex and lsnOrder.
// Handles empty WALs gracefully (no error on first startup).
func (pw *PoTSWAL) rebuildIndex() error {
	// Check if WAL has any entries at all
	firstIndex, err := pw.wal.Log.FirstIndex()
	if err != nil {
		return fmt.Errorf("failed to get first index: %w", err)
	}
	lastIndex, err := pw.wal.Log.LastIndex()
	if err != nil {
		return fmt.Errorf("failed to get last index: %w", err)
	}

	// Empty WAL - nothing to rebuild
	if firstIndex == 0 && lastIndex == 0 {
		return nil
	}

	return pw.wal.ReplayEventsByType(wal_types.PoTS, func(entry WAL.WALEntry) error {
		ev := &WAL.PoTSBlockEvent{}
		if err := ev.Deserialize(entry.Data); err != nil {
			// Skip corrupt entries — do not abort a startup replay.
			return nil
		}
		pw.blockIndex[ev.BlockNumber] = entry.LSN
		pw.lsnOrder = append(pw.lsnOrder, entry.LSN)
		if ev.BlockNumber > pw.latestBlock {
			pw.latestBlock = ev.BlockNumber
		}
		return nil
	})
}

// Write appends a single ZKBlock to the WAL.
func (pw *PoTSWAL) Write(ctx context.Context, block *types.ZKBlock) error {
	if block == nil {
		return fmt.Errorf("pots WAL: block is nil")
	}

	data, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("pots WAL: failed to marshal block %d: %w", block.BlockNumber, err)
	}

	ev := &WAL.PoTSBlockEvent{
		BaseEvent:   wal_types.BaseEvent{Operation: wal_types.OpAppend},
		BlockNumber: block.BlockNumber,
		BlockData:   data,
	}

	lsn, err := pw.wal.WriteEvent(ev)
	if err != nil {
		return fmt.Errorf("pots WAL: WAL write failed for block %d: %w", block.BlockNumber, err)
	}

	if err := pw.wal.Flush(); err != nil {
		return fmt.Errorf("pots WAL: flush failed for block %d: %w", block.BlockNumber, err)
	}

	pw.mu.Lock()
	pw.blockIndex[block.BlockNumber] = lsn
	pw.lsnOrder = append(pw.lsnOrder, lsn)
	if block.BlockNumber > pw.latestBlock {
		pw.latestBlock = block.BlockNumber
	}
	pw.mu.Unlock()

	return nil
}

// WriteBatch appends multiple ZKBlocks to the WAL and flushes once at the end.
// Blocks are written sequentially; a single Flush makes the entire batch durable.
func (pw *PoTSWAL) WriteBatch(ctx context.Context, blocks []*types.ZKBlock) error {
	if len(blocks) == 0 {
		return nil
	}

	type written struct {
		blockNumber uint64
		lsn         uint64
	}
	committed := make([]written, 0, len(blocks))

	for _, block := range blocks {
		if block == nil {
			continue
		}

		data, err := json.Marshal(block)
		if err != nil {
			return fmt.Errorf("pots WAL: failed to marshal block %d: %w", block.BlockNumber, err)
		}

		ev := &WAL.PoTSBlockEvent{
			BaseEvent:   wal_types.BaseEvent{Operation: wal_types.OpAppend},
			BlockNumber: block.BlockNumber,
			BlockData:   data,
		}

		lsn, err := pw.wal.WriteEvent(ev)
		if err != nil {
			return fmt.Errorf("pots WAL: WAL write failed for block %d: %w", block.BlockNumber, err)
		}
		committed = append(committed, written{block.BlockNumber, lsn})
	}

	if err := pw.wal.Flush(); err != nil {
		return fmt.Errorf("pots WAL: flush failed after batch: %w", err)
	}

	pw.mu.Lock()
	for _, w := range committed {
		pw.blockIndex[w.blockNumber] = w.lsn
		pw.lsnOrder = append(pw.lsnOrder, w.lsn)
		if w.blockNumber > pw.latestBlock {
			pw.latestBlock = w.blockNumber
		}
	}
	pw.mu.Unlock()

	return nil
}

// Read returns blocks in insertion order, skipping the first offset entries and
// returning at most limit blocks. Used by the hydration reader to page through
// the WAL from the front.
func (pw *PoTSWAL) Read(ctx context.Context, offset, limit int) ([]*types.ZKBlock, error) {
	pw.mu.RLock()
	total := len(pw.lsnOrder)
	pw.mu.RUnlock()

	if offset >= total {
		return nil, nil
	}

	pw.mu.RLock()
	end := offset + limit
	if end > total {
		end = total
	}
	lsns := make([]uint64, end-offset)
	copy(lsns, pw.lsnOrder[offset:end])
	pw.mu.RUnlock()

	return pw.readByLSNs(lsns)
}

// ReadByRange returns all blocks whose block number falls within [startBlock, endBlock].
// Uses the in-memory index for O(n_range) lookup — no WAL scan needed.
func (pw *PoTSWAL) ReadByRange(ctx context.Context, startBlock, endBlock uint64) ([]*types.ZKBlock, error) {
	pw.mu.RLock()
	lsns := make([]uint64, 0)
	for bn := startBlock; bn <= endBlock; bn++ {
		if lsn, ok := pw.blockIndex[bn]; ok {
			lsns = append(lsns, lsn)
		}
	}
	pw.mu.RUnlock()

	return pw.readByLSNs(lsns)
}

// HasBlock reports whether a block with the given number is already in the WAL.
// Used during Phase 2 deduplication.
func (pw *PoTSWAL) HasBlock(blockNumber uint64) bool {
	pw.mu.RLock()
	_, ok := pw.blockIndex[blockNumber]
	pw.mu.RUnlock()
	return ok
}

// GetLatestBlockNumber returns the highest block number currently in the WAL.
// Returns 0 if the WAL is empty.
func (pw *PoTSWAL) GetLatestBlockNumber(_ context.Context) (uint64, error) {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	return pw.latestBlock, nil
}

// Count returns the total number of blocks stored in the WAL.
func (pw *PoTSWAL) Count(_ context.Context) (uint64, error) {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	return uint64(len(pw.lsnOrder)), nil
}

// Close flushes any pending WAL buffer and releases the underlying log handle.
func (pw *PoTSWAL) Close() error {
	if err := pw.wal.Flush(); err != nil {
		return fmt.Errorf("pots WAL: flush on close failed: %w", err)
	}
	return pw.wal.Log.Close()
}

// ----------------------------------------------------------------------------
// Internal helpers
// ----------------------------------------------------------------------------

// readByLSNs reads WAL entries for the given LSNs and deserializes them into ZKBlocks.
func (pw *PoTSWAL) readByLSNs(lsns []uint64) ([]*types.ZKBlock, error) {
	if len(lsns) == 0 {
		return nil, nil
	}

	blocks := make([]*types.ZKBlock, 0, len(lsns))
	for _, lsn := range lsns {
		entry, err := pw.wal.GetEvent(lsn)
		if err != nil {
			return nil, fmt.Errorf("pots WAL: read LSN %d failed: %w", lsn, err)
		}

		ev := &WAL.PoTSBlockEvent{}
		if err := ev.Deserialize(entry.Data); err != nil {
			return nil, fmt.Errorf("pots WAL: deserialize LSN %d failed: %w", lsn, err)
		}

		var block types.ZKBlock
		if err := json.Unmarshal(ev.BlockData, &block); err != nil {
			return nil, fmt.Errorf("pots WAL: unmarshal block at LSN %d failed: %w", lsn, err)
		}

		blocks = append(blocks, &block)
	}

	return blocks, nil
}

// ensure *PoTSWAL satisfies the interface at compile time.
var _ PoTS_WAL = (*PoTSWAL)(nil)
