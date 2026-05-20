package WAL

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
)

const (
	asyncFlushInterval = 60 * time.Millisecond
	asyncIdleTimeout   = 10 * time.Second
)

type entryKind uint8

const (
	kindData       entryKind = iota
	kindCheckpoint
)

// pendingEntry holds a pre-serialised WAL event waiting to be assigned an LSN.
// Serialisation happens at enqueue time (no lock); LSN is assigned at flush time
// inside the WAL mutex so ordering is always correct even with multiple buffers.
type pendingEntry struct {
	walType wal_types.WALType
	data    []byte
}

// stackEntry is one node in the coalescing stack.
// Adjacent data pushes are merged into a single node; checkpoint nodes act as
// barriers and are deduplicated when consecutive.
type stackEntry struct {
	kind    entryKind
	entries []pendingEntry // non-nil for kindData only
}

// AsyncBuffer is a coalescing write buffer that batches WAL entries and flushes
// them to the underlying WAL every asyncFlushInterval.
//
// Merge rules (applied at push time against the current stack top):
//   push data       + top=data        → append into top entry (merge)
//   push data       + top=checkpoint  → new data node
//   push checkpoint + top=checkpoint  → deduplicate (no-op)
//   push checkpoint + top=data        → new checkpoint node
//
// The buffer self-closes after asyncIdleTimeout of inactivity: it drains,
// CAS-nils the global slot, and exits. The next AddToBufferWAL call creates a
// fresh buffer transparently.
type AsyncBuffer struct {
	mu     sync.Mutex
	stack  []stackEntry
	closed bool

	wal    *WAL
	global *atomic.Pointer[AsyncBuffer]

	done      chan struct{}
	drainDone chan struct{}
	closeOnce sync.Once

	lastActivity atomic.Int64 // unix nanoseconds of last push
}

func newAsyncBuffer(w *WAL, global *atomic.Pointer[AsyncBuffer]) *AsyncBuffer {
	b := &AsyncBuffer{
		wal:       w,
		global:    global,
		done:      make(chan struct{}),
		drainDone: make(chan struct{}),
	}
	b.lastActivity.Store(time.Now().UnixNano())
	return b
}

// pushData enqueues a pre-serialised entry.
// Returns false if the buffer is already closed; the caller must create a new one.
func (b *AsyncBuffer) pushData(e pendingEntry) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	if len(b.stack) > 0 && b.stack[len(b.stack)-1].kind == kindData {
		b.stack[len(b.stack)-1].entries = append(b.stack[len(b.stack)-1].entries, e)
	} else {
		b.stack = append(b.stack, stackEntry{kind: kindData, entries: []pendingEntry{e}})
	}
	b.lastActivity.Store(time.Now().UnixNano())
	return true
}

// pushCheckpoint enqueues a checkpoint sentinel.
// Returns false if the buffer is closed.
func (b *AsyncBuffer) pushCheckpoint() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	if len(b.stack) > 0 && b.stack[len(b.stack)-1].kind == kindCheckpoint {
		// consecutive checkpoints collapse to one
		return true
	}
	b.stack = append(b.stack, stackEntry{kind: kindCheckpoint})
	b.lastActivity.Store(time.Now().UnixNano())
	return true
}

// flushAndReport swaps the stack and writes it to WAL.
// Returns true if there was anything to flush.
func (b *AsyncBuffer) flushAndReport() bool {
	b.mu.Lock()
	if len(b.stack) == 0 {
		b.mu.Unlock()
		return false
	}
	snapshot := b.stack
	b.stack = make([]stackEntry, 0, cap(snapshot))
	b.mu.Unlock()

	b.writeToWAL(snapshot)
	return true
}

// closeAndDrain marks the buffer closed, CAS-nils the global slot so new writers
// immediately see a nil slot and create a fresh buffer, then writes any remaining
// stack entries to WAL and signals drainDone.
func (b *AsyncBuffer) closeAndDrain() {
	b.mu.Lock()
	b.closed = true
	snapshot := b.stack
	b.stack = nil
	b.mu.Unlock()

	// Unregister before flush: new writers can create a fresh buffer in parallel
	// while this buffer is draining.
	b.global.CompareAndSwap(b, nil)

	b.writeToWAL(snapshot)
	close(b.drainDone)
}

// writeToWAL processes a stack snapshot into the underlying WAL.
// For kindData nodes: acquires WAL.Mu, assigns LSNs, appends to WAL buffer,
// then calls flushBuffer once per node.
// For kindCheckpoint nodes: calls WAL.CreateCheckpoint (has its own lock).
func (b *AsyncBuffer) writeToWAL(snapshot []stackEntry) {
	for _, se := range snapshot {
		switch se.kind {
		case kindData:
			b.wal.Mu.Lock()
			for _, pe := range se.entries {
				lsn := b.wal.nextLSN()
				b.wal.Buffer = append(b.wal.Buffer, WALEntry{
					Type:      pe.walType,
					Data:      pe.data,
					LSN:       lsn,
					Timestamp: time.Now().Unix(),
				})
				if len(b.wal.Buffer) >= b.wal.BatchSize {
					if err := b.wal.flushBuffer(); err != nil {
						log.Printf("async WAL: mid-batch flush error: %v", err)
					}
				}
			}
			var flushErr error
			var lastLSN uint64
			if flushErr = b.wal.flushBuffer(); flushErr != nil {
				log.Printf("async WAL: flush error: %v", flushErr)
			} else {
				lastLSN = b.wal.lastFlushedLSN
			}
			b.wal.Mu.Unlock()
			if flushErr == nil {
				log.Printf("[WAL] %d coalesced entries committed to disk — last LSN %d",
					len(se.entries), lastLSN)
			}

		case kindCheckpoint:
			if _, err := b.wal.CreateCheckpoint(); err != nil {
				log.Printf("async WAL: checkpoint error: %v", err)
			}
		}
	}
}

// Drain signals the buffer to stop accepting writes, flushes all remaining
// entries to WAL, and blocks until complete. Safe to call multiple times.
func (b *AsyncBuffer) Drain() {
	b.closeOnce.Do(func() { close(b.done) })
	<-b.drainDone
}

// run is the background goroutine: periodic flush every asyncFlushInterval,
// idle shutdown after asyncIdleTimeout of inactivity.
func (b *AsyncBuffer) run() {
	ticker := time.NewTicker(asyncFlushInterval)
	defer ticker.Stop()

	idleTimer := time.NewTimer(asyncIdleTimeout)
	defer idleTimer.Stop()

	resetIdle := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(asyncIdleTimeout)
	}

	for {
		select {
		case <-ticker.C:
			if b.flushAndReport() {
				resetIdle()
			}

		case <-idleTimer.C:
			since := time.Since(time.Unix(0, b.lastActivity.Load()))
			if since < asyncIdleTimeout {
				// spurious fire (activity happened after timer was set)
				idleTimer.Reset(asyncIdleTimeout - since)
				continue
			}
			b.closeAndDrain()
			return

		case <-b.done:
			b.closeAndDrain()
			return
		}
	}
}
