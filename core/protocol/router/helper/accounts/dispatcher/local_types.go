package dispatcher

import (
	"sync"
	"sync/atomic"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// noncePage is the internal unit of work flowing through the dispatcher.
// It is private — callers only interact with []uint64 nonce slices.
//
// PageIndex=0 means unassigned; workers assign a 1-based index on first attempt.
// Retries is incremented on each failed delivery attempt.
type noncePage struct {
	Nonces    []uint64
	Retries   int
	PageIndex uint32
}

// AccountDispatcher is a session-scoped async streaming dispatcher.
//
// # Isolation guarantee
//
// One AccountDispatcher is created per incoming AccountSync connection.
// All internal state (channel, counters, condition variable, dead-letter list)
// is per-instance. Concurrent sessions running on the same server never share
// any state — the diff stage's WaitForBatchStart operates against its own
// instance's counter, not a global one.
//
// # Buffer contract
//
// The nonceChan always accepts data regardless of the soft pause threshold.
// Channel capacity is sized to absorb the worst-case overshoot: all
// NumDiffParents goroutines completing their current batch simultaneously
// when the pause threshold fires:
//
//	cap = ceil((pauseThreshold + numDiffParents × pageSize) / pageSize)
//	    = ceil((100k + 30×3k) / 3k) = ceil(63.3) = 64 → use 70 for margin
//
// EnqueueNonces is therefore non-blocking in all realistic scenarios.
// Context cancellation is the only reason EnqueueNonces can return an error.
//
// # Flow control
//
// The diff stage is the "tap". Before pulling each new batch from its work
// channel, it calls WaitForBatchStart(ctx). If nonceBufferCount ≥
// pauseThreshold, WaitForBatchStart sleeps until the count drops below
// 80 % of pauseThreshold (hysteresis). The current in-flight batch always
// finishes and pushes freely — only the next batch is gated.
//
// # Lifecycle
//
//	d, _ := NewAccountDispatcher(cfg, callbacks)
//	go func() {
//	    summary, err := d.Run(ctx)   // starts workers; blocks until done
//	    ...
//	}()
//	for _, page := range nonces {
//	    d.WaitForBatchStart(ctx)     // pause gate (diff stage calls this)
//	    d.EnqueueNonces(ctx, page)   // always-accepting push
//	}
//	d.CloseInput()                   // signal: no more nonce pages coming
//	// Run() returns once every page is delivered or dead-lettered.
type AccountDispatcher struct {
	cfg       types.DispatcherConfig
	callbacks types.DispatcherCallbacks

	// nonceChan is the central buffer between the diff stage and the dispatch
	// workers. It is NEVER closed — workers watch the `done` channel instead.
	// Keeping it open removes the close+send race that closing a channel introduces.
	nonceChan chan noncePage

	// Lifecycle channels and guards.
	done        chan struct{} // closed exactly once when input is closed AND inflight==0
	doneOnce    sync.Once
	inputClosed atomic.Bool  // set by CloseInput
	inflight    atomic.Int64 // pages admitted − pages finalised (delivered or dead-lettered)

	// Backpressure gate.
	// pauseMu + pauseCond implement the hysteresis: diff goroutines sleep on
	// pauseCond when nonceBufferCount ≥ pauseThreshold and resume when it
	// drops below resumeThreshold (80 % of pauseThreshold).
	pauseMu          sync.Mutex
	pauseCond        *sync.Cond
	nonceBufferCount atomic.Int64 // nonces currently sitting in nonceChan

	// Page sequence counter — assigns monotonically increasing 1-based page
	// indices to pages on their first dispatch attempt.
	pageSequence atomic.Uint32

	// Result accumulators (written by workers, read once after Run returns).
	deliveredPages    atomic.Uint32
	deliveredAccounts atomic.Uint64

	// Dead-letter list — pages that exhausted all retries.
	// Protected by deadMu; appended by workers, read once after Run returns.
	deadMu      sync.Mutex
	deadLetters []types.DeadLetterPage
}

