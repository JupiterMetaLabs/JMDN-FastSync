package WAL

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/tidwall/wal"
)

const (
	lsnMetadataFile = "lsn_metadata.json"
)

// LSNMetadata stores the current LSN state for recovery
type LSNMetadata struct {
	CurrentLSN     uint64 `json:"current_lsn"`
	LastFlushedLSN uint64 `json:"last_flushed_lsn"`
	LastUpdated    int64  `json:"last_updated"`
}

// NewWAL creates a new WAL (Write-Ahead Log) instance.
// Parameters:
//   - dir: The directory where WAL files and metadata will be stored.
//   - batchSize: The number of events to buffer before auto-flushing to disk.
//
// Returns a pointer to the WAL instance or an error if initialization fails.
func NewWAL(dir string, batchSize int) (*WAL, error) {
	if batchSize <= 0 {
		batchSize = wal_types.DefaultBatchSize
	}

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	// Configure WAL options (e.g., 100MB segments)
	opts := &wal.Options{
		SegmentSize: wal_types.SegmentSizeLimit,
	}

	// Open the WAL log
	log, err := wal.Open(dir, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL: %w", err)
	}

	w := &WAL{
		Dir:       dir,
		Log:       log,
		Buffer:    make([]WALEntry, 0, batchSize),
		BatchSize: batchSize,
	}

	// Ensure checkpoint directory exists
	checkpointPath := filepath.Join(dir, wal_types.CheckpointDir)
	if err := os.MkdirAll(checkpointPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Load or initialize LSN
	if err := w.loadLSN(); err != nil {
		return nil, fmt.Errorf("failed to load LSN: %w", err)
	}

	return w, nil
}

// loadLSN attempts to load the current LSN state from the metadata file.
// If the file doesn't exist (new WAL), it initializes the LSN to 0.
// This is called internally during NewWAL.
func (w *WAL) loadLSN() error {
	metadataPath := filepath.Join(w.Dir, lsnMetadataFile)

	data, err := os.ReadFile(metadataPath)
	if os.IsNotExist(err) {
		// Initialize LSN to 0 for new WAL
		w.currentLSN.Store(0)
		w.lastFlushedLSN = 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read LSN metadata: %w", err)
	}

	var metadata LSNMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("failed to unmarshal LSN metadata: %w", err)
	}

	w.currentLSN.Store(metadata.CurrentLSN)
	w.lastFlushedLSN = metadata.LastFlushedLSN

	return nil
}

// saveLSN persists the current LSN and flush state to a JSON metadata file.
// This ensures that after a crash, the WAL knows where it left off.
func (w *WAL) saveLSN() error {
	metadata := LSNMetadata{
		CurrentLSN:     w.currentLSN.Load(),
		LastFlushedLSN: w.lastFlushedLSN,
		LastUpdated:    time.Now().Unix(),
	}

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal LSN metadata: %w", err)
	}

	metadataPath := filepath.Join(w.Dir, lsnMetadataFile)
	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write LSN metadata: %w", err)
	}

	return nil
}

// nextLSN generates the next logical sequence number in a thread-safe manner.
func (w *WAL) nextLSN() uint64 {
	return w.currentLSN.Add(1)
}

// WriteEvent accepts an EventAdapter, assigns it an LSN, and adds it to the buffer.
// If the buffer reaches BatchSize, it triggers an automatic flush to disk.
// Returns the assigned LSN or an error if serialization/flushing fails.
func (w *WAL) WriteEvent(event wal_types.EventAdapter) (uint64, error) {
	w.Mu.Lock()
	defer w.Mu.Unlock()

	// Assign LSN to the event
	lsn := w.nextLSN()

	// Serialize the event
	data, err := event.Serialize()
	if err != nil {
		return 0, fmt.Errorf("failed to serialize event: %w", err)
	}

	// Create WAL entry
	entry := WALEntry{
		Type:      event.GetType(),
		Data:      data,
		LSN:       lsn,
		Timestamp: time.Now().Unix(),
	}

	// Add to buffer
	w.Buffer = append(w.Buffer, entry)

	// Flush if buffer is full
	if len(w.Buffer) >= w.BatchSize {
		if err := w.flushBuffer(); err != nil {
			return 0, fmt.Errorf("failed to flush buffer: %w", err)
		}
	}

	return lsn, nil
}

// flushBuffer performs the actual disk I/O, writing all buffered entries to the log.
// It also updates the lastFlushedLSN and persists the metadata.
func (w *WAL) flushBuffer() error {
	if len(w.Buffer) == 0 {
		return nil
	}

	// Serialize all entries
	for _, entry := range w.Buffer {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("failed to marshal entry: %w", err)
		}

		// Write to WAL
		if err := w.Log.Write(entry.LSN, data); err != nil {
			return fmt.Errorf("failed to write to WAL: %w", err)
		}

		// Update last flushed LSN
		if entry.LSN > w.lastFlushedLSN {
			w.lastFlushedLSN = entry.LSN
		}
	}

	// Clear buffer
	w.Buffer = w.Buffer[:0]

	// Persist LSN metadata
	if err := w.saveLSN(); err != nil {
		return fmt.Errorf("failed to save LSN: %w", err)
	}

	return nil
}

// Flush manually triggers a write of all buffered events to the disk.
// Use this when you need to ensure data persistence before a specific operation.
func (w *WAL) Flush() error {
	w.Mu.Lock()
	defer w.Mu.Unlock()
	return w.flushBuffer()
}

// ReplayEvents iterates through all events stored in the WAL from the beginning.
// For each event, it calls the provided handler function.
// This is the core mechanism for state hydration/recovery.
func (w *WAL) ReplayEvents(handler func(entry WALEntry) error) error {
	w.Mu.RLock()
	defer w.Mu.RUnlock()

	// Get the first and last indexes
	firstIndex, err := w.Log.FirstIndex()
	if err != nil {
		return fmt.Errorf("failed to get first index: %w", err)
	}

	lastIndex, err := w.Log.LastIndex()
	if err != nil {
		return fmt.Errorf("failed to get last index: %w", err)
	}

	// Replay events in order
	for index := firstIndex; index <= lastIndex; index++ {
		data, err := w.Log.Read(index)
		if err != nil {
			return fmt.Errorf("failed to read entry at index %d: %w", index, err)
		}

		var entry WALEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("failed to unmarshal entry: %w", err)
		}

		// Call handler for each event
		if err := handler(entry); err != nil {
			return fmt.Errorf("handler failed for LSN %d: %w", entry.LSN, err)
		}
	}

	return nil
}

// ReplayEventsByType is a filtered version of ReplayEvents that only
// processes events matching the specified WALType.
func (w *WAL) ReplayEventsByType(walType wal_types.WALType, handler func(entry WALEntry) error) error {
	return w.ReplayEvents(func(entry WALEntry) error {
		if entry.Type == walType {
			return handler(entry)
		}
		return nil
	})
}

// GetEvent retrieves a single WALEntry by its LSN.
// It first checks the in-memory buffer (for unflushed entries),
// then falls back to reading from the on-disk WAL.
func (w *WAL) GetEvent(lsn uint64) (*WALEntry, error) {
	w.Mu.RLock()
	defer w.Mu.RUnlock()

	// 1. Check the in-memory buffer first (unflushed entries)
	for i := range w.Buffer {
		if w.Buffer[i].LSN == lsn {
			entry := w.Buffer[i] // copy
			return &entry, nil
		}
	}

	// 2. Read from disk — LSN is used as the index in tidwall/wal
	data, err := w.Log.Read(lsn)
	if err != nil {
		return nil, fmt.Errorf("failed to read WAL entry at LSN %d: %w", lsn, err)
	}

	var entry WALEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal WAL entry at LSN %d: %w", lsn, err)
	}

	return &entry, nil
}

// GetLastLSN returns the most recently assigned LSN (may not be on disk yet).
func (w *WAL) GetLastLSN() uint64 {
	return w.currentLSN.Load()
}

// GetLastFlushedLSN returns the highest LSN that is guaranteed to be on disk.
func (w *WAL) GetLastFlushedLSN() uint64 {
	w.Mu.RLock()
	defer w.Mu.RUnlock()
	return w.lastFlushedLSN
}

// Close ensures all buffered data is flushed before closing the underlying log.
func (w *WAL) Close() error {
	w.Mu.Lock()
	defer w.Mu.Unlock()

	// Flush remaining buffer
	if err := w.flushBuffer(); err != nil {
		return fmt.Errorf("failed to flush on close: %w", err)
	}

	// Close the WAL
	if err := w.Log.Close(); err != nil {
		return fmt.Errorf("failed to close WAL: %w", err)
	}

	return nil
}

// TruncateBefore eliminates all log entries with an LSN lower than the specified value.
// CAUTION: This is a destructive operation. Use only after state is safely checkpointed.
func (w *WAL) TruncateBefore(lsn uint64) error {
	w.Mu.Lock()
	defer w.Mu.Unlock()
	return w.truncateBeforeUnlocked(lsn)
}

// truncateBeforeUnlocked is the non-locking version of TruncateBefore
func (w *WAL) truncateBeforeUnlocked(lsn uint64) error {

	// Find the corresponding index for the LSN
	firstIndex, err := w.Log.FirstIndex()
	if err != nil {
		return fmt.Errorf("failed to get first index: %w", err)
	}

	lastIndex, err := w.Log.LastIndex()
	if err != nil {
		return fmt.Errorf("failed to get last index: %w", err)
	}

	// Find the index to truncate to
	var truncateIndex uint64
	for index := firstIndex; index <= lastIndex; index++ {
		data, err := w.Log.Read(index)
		if err != nil {
			continue
		}

		var entry WALEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue
		}

		if entry.LSN >= lsn {
			truncateIndex = index
			break
		}
	}

	if truncateIndex > 0 {
		if err := w.Log.TruncateFront(truncateIndex); err != nil {
			return fmt.Errorf("failed to truncate: %w", err)
		}
	}

	return nil
}

// CreateCheckpoint creates a new LSN marker and manages checkpoint rotation.
// It ensures that only MaxSnapshotLimit checkpoints exist and truncates the WAL
// up to the oldest checkpoint's LSN when the limit is reached.
// We are locking in the CreateCheckpoint() function so we are not locking again in the truncate functon.
func (w *WAL) CreateCheckpoint() (uint64, error) {
	w.Mu.Lock()
	defer w.Mu.Unlock()

	// 1. Ensure any buffered events are flushed before checkpointing
	if err := w.flushBuffer(); err != nil {
		return 0, fmt.Errorf("failed to flush before checkpoint: %w", err)
	}

	lsn := w.lastFlushedLSN
	checkpointPath := filepath.Join(w.Dir, wal_types.CheckpointDir)
	newCheckpointFile := filepath.Join(checkpointPath, fmt.Sprintf("lsn_%020d", lsn))

	// 2. Create the new checkpoint marker
	if err := os.WriteFile(newCheckpointFile, []byte(fmt.Sprintf("%d", lsn)), 0644); err != nil {
		return 0, fmt.Errorf("failed to create checkpoint marker: %w", err)
	}

	// 3. Manage rotation
	files, err := os.ReadDir(checkpointPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read checkpoints: %w", err)
	}

	// Filter and count checkpoint files
	var checkpointFiles []string
	for _, f := range files {
		if !f.IsDir() && len(f.Name()) > 4 && f.Name()[:4] == "lsn_" {
			checkpointFiles = append(checkpointFiles, f.Name())
		}
	}

	// If we exceed the limit, truncate and remove the oldest
	if len(checkpointFiles) > wal_types.MaxSnapshotLimit {
		// Files are named lsn_%020d, so sorting them Lexicographically is same as sorting by LSN
		// os.ReadDir already returns them sorted by name
		oldestFile := checkpointFiles[0]
		var oldestLSN uint64
		fmt.Sscanf(oldestFile, "lsn_%d", &oldestLSN)

		// Truncate WAL up to the oldest LSN
		if err := w.truncateBeforeUnlocked(oldestLSN); err != nil {
			return 0, fmt.Errorf("failed to truncate oldest checkpoint: %w", err)
		}

		// Remove the oldest marker file
		if err := os.Remove(filepath.Join(checkpointPath, oldestFile)); err != nil {
			return 0, fmt.Errorf("failed to remove oldest checkpoint marker: %w", err)
		}
	}

	return lsn, nil
}
