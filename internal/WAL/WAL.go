package WAL

import (
	"sync"

	"github.com/tidwall/wal"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/wal"
)

type WAL struct {

	// Type is for the type of request. example Headers sync, merkle sync, prior sync, actual data chunks download etc
	Type wal_types.WALType

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
