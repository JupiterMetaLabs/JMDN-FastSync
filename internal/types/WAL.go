package types

import (
	"os"
	"sync"
)

const(
	DefaultBatchSize = 1000
	DefaultDir = "tmp/wal"
)

type WAL struct {
	// Dir is the directory path where WAL files are stored
	Dir string

	// File is the current active open file for writing
	File *os.File

	// Buffer holds the block headers in memory (e.g., up to 1000 items)
	// We store them as byte slices (serialized data)
	Buffer [][]byte

	// BatchSize is the threshold (e.g., 1000) to trigger a flush to disk
	BatchSize int

	// Mu protects the WAL state for concurrent access
	Mu sync.RWMutex
}
