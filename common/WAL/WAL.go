package WAL

import (
	"sync"
	"sync/atomic"

	"github.com/tidwall/wal"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
)

// WAL represents the Write-Ahead Log for event sourcing
type WAL struct {
	// Dir is the directory path where WAL files are stored
	Dir string

	// Log is the WAL handle from tidwall/wal
	Log *wal.Log

	// Buffer holds events in memory before flushing
	Buffer []WALEntry

	// BatchSize is the threshold to trigger a flush to disk
	BatchSize int

	// currentLSN is the global Log Sequence Number
	// This is atomic to ensure thread-safe increments across goroutines
	currentLSN atomic.Uint64

	// Mu protects the WAL state for concurrent access
	Mu sync.RWMutex

	// lastFlushedLSN tracks the last LSN that was persisted to disk
	lastFlushedLSN uint64
}

// WALEntry represents a single event in the WAL
type WALEntry struct {
	// Type is the event type (HeaderSync, MerkleSync, etc.)
	Type wal_types.WALType `json:"type"`

	// Data is the serialized event data
	Data []byte `json:"data"`

	// LSN is the Log Sequence Number - a monotonically increasing counter
	// that uniquely identifies this event across all WAL files
	LSN uint64 `json:"lsn"`

	// Timestamp when the event was written
	Timestamp int64 `json:"timestamp,omitempty"`
}