package WAL

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tidwall/wal"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/wal"
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

// NewWAL creates a new WAL instance with LSN tracking
func NewWAL(dir string, batchSize int) (*WAL, error) {
	if batchSize <= 0 {
		batchSize = wal_types.DefaultBatchSize
	}

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	// Open the WAL log
	log, err := wal.Open(dir, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL: %w", err)
	}

	w := &WAL{
		Dir:       dir,
		Log:       log,
		Buffer:    make([]WALEntry, 0, batchSize),
		BatchSize: batchSize,
	}

	// Load or initialize LSN
	if err := w.loadLSN(); err != nil {
		return nil, fmt.Errorf("failed to load LSN: %w", err)
	}

	return w, nil
}

// loadLSN loads the LSN from metadata file or initializes it
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

// saveLSN persists the current LSN to metadata file
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

// nextLSN atomically increments and returns the next LSN
func (w *WAL) nextLSN() uint64 {
	return w.currentLSN.Add(1)
}

// WriteEvent writes an event to the WAL using the adapter pattern
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

// flushBuffer writes all buffered events to the WAL
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

// Flush forces a flush of the buffer to disk
func (w *WAL) Flush() error {
	w.Mu.Lock()
	defer w.Mu.Unlock()
	return w.flushBuffer()
}

// ReplayEvents reads all events from the WAL in LSN order for hydration
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

// ReplayEventsByType replays events of a specific type
func (w *WAL) ReplayEventsByType(walType wal_types.WALType, handler func(entry WALEntry) error) error {
	return w.ReplayEvents(func(entry WALEntry) error {
		if entry.Type == walType {
			return handler(entry)
		}
		return nil
	})
}

// GetLastLSN returns the current LSN value
func (w *WAL) GetLastLSN() uint64 {
	return w.currentLSN.Load()
}

// GetLastFlushedLSN returns the last LSN that was flushed to disk
func (w *WAL) GetLastFlushedLSN() uint64 {
	w.Mu.RLock()
	defer w.Mu.RUnlock()
	return w.lastFlushedLSN
}

// Close flushes any remaining data and closes the WAL
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

// TruncateBefore removes all entries before the given LSN
// This is useful for pruning old events after successful hydration
func (w *WAL) TruncateBefore(lsn uint64) error {
	w.Mu.Lock()
	defer w.Mu.Unlock()

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
