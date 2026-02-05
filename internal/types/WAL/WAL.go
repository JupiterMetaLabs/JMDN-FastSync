package WAL

import (
	"sync"

	"github.com/tidwall/wal"
)

const (
	DefaultBatchSize = 1000
	DefaultDir       = "tmp/wal"
)

type WAL struct {
	// Dir is the directory path where WAL files are stored
	Dir string

	// Log is the WAL handle from tidwall/wal
	Log *wal.Log

	// Buffer holds the block headers in memory (e.g., up to 1000 items)
	// We store them as byte slices (serialized data)
	Buffer [][]byte

	// BatchSize is the threshold (e.g., 1000) to trigger a flush to disk
	BatchSize int

	// Mu protects the WAL state for concurrent access
	Mu sync.RWMutex
}
