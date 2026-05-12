package wal

type WALType string

const (
	HeaderSync     WALType = "wal:headersync"
	MerkleSync     WALType = "wal:merklesync"
	PriorSync      WALType = "wal:priorsync"
	DataSync       WALType = "wal:datasync"
	Reconciliation WALType = "wal:reconciliation"
	PoTS           WALType = "wal:pots"
	AccountSync    WALType = "wal:accountsync"
)

const (
	DefaultBatchSize = 1000
	DefaultDir       = "./internal/WAL/.tmp/wal"
	SegmentSizeLimit = 100 * 1024 * 1024 // 100MB
	MaxSnapshotLimit = 10                // Keep 10 most recent checkpoints
	CheckpointDir    = "checkpoint"
)

// EventOperation defines the type of operation in event sourcing
type EventOperation string

const (
	OpAppend           EventOperation = "APPEND"            // Add new data
	OpSync             EventOperation = "SYNC"              // Synchronization checkpoint
	OpUpdate           EventOperation = "UPDATE"            // Update existing data
	OpDelete           EventOperation = "DELETE"            // Delete existing data
	OpDownload         EventOperation = "Download"          // download checkpoint
	OpRequest          EventOperation = "REQUEST"           // request checkpoint
	OpDeleteCheckpoint EventOperation = "DELETE_CHECKPOINT" // delete checkpoint
	OpCheckpoint       EventOperation = "CHECKPOINT"        // checkpoint
)

// EventAdapter defines the interface for writing different event types to WAL
// This follows the Adapter Pattern to handle various event types uniformly
type EventAdapter interface {
	// GetType returns the WAL type for this event
	GetType() WALType

	// GetOperation returns the event operation type
	GetOperation() EventOperation

	// Serialize converts the event to bytes for storage
	Serialize() ([]byte, error)

	// Deserialize reconstructs the event from bytes
	Deserialize(data []byte) error
}

// BaseEvent provides common functionality for all events
type BaseEvent struct {
	Type      WALType        `json:"type"`
	LSN       uint64         `json:"lsn"`
	Operation EventOperation `json:"operation"`
}

// GetWALDirSeparate returns separate directories per type (not recommended)
// Only use if you don't need global LSN ordering across event types
// Deprecated: Use GetWALDir() instead for proper event sourcing
func GetWALDirSeparate(walType WALType) string {
	switch walType {
	case HeaderSync:
		return DefaultDir + "/headers"
	case MerkleSync:
		return DefaultDir + "/merkle"
	case PriorSync:
		return DefaultDir + "/prior"
	case DataSync:
		return DefaultDir + "/data"
	default:
		return DefaultDir
	}
}
