package dispatcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
)



// New constructs a ready-to-use AccountDispatcher.
//
// Channel capacity is computed at construction time as:
//
//	ceil((pauseThreshold + numDiffParents × pageSize) / pageSize)
//
// This absorbs the worst-case overshoot where all diff parents complete
// their current batch simultaneously when the pause threshold fires.
//
// Returns an error if cfg.Validate() fails or required callbacks are nil.
//
// Time: O(chanCap) to allocate the channel. Space: O(chanCap).
func New(cfg types.DispatcherConfig, callbacks types.DispatcherCallbacks) (*AccountDispatcher, error) {
	if err := validateDispatcherConfig(cfg, callbacks); err != nil {
		return nil, err
	}

	// Channel capacity: absorb worst-case overshoot from all diff parents
	// completing their current batch when the pause threshold fires.
	// Formula: ceil((pauseThreshold + numDiffParents × pageSize) / pageSize)
	// With defaults: ceil((100k + 30×3k) / 3k) = 64 → padded to 70.
	worstCaseNonces := cfg.NonceBufferPauseThreshold + int64(constants.NumDiffParents)*int64(cfg.NoncePageSize)
	chanCap := int((worstCaseNonces + int64(cfg.NoncePageSize) - 1) / int64(cfg.NoncePageSize))

	d := &AccountDispatcher{
		cfg:       cfg,
		callbacks: callbacks,
		nonceChan: make(chan noncePage, chanCap),
		done:      make(chan struct{}),
	}
	d.pauseCond = sync.NewCond(&d.pauseMu)
	return d, nil
}

// ─── Buffer API (called by the diff stage) ────────────────────────────────────

// WaitForBatchStart is the flow-control gate for the diff stage.
//
// The diff stage MUST call this before pulling each new batch from its work
// channel (workChan). If the number of nonces currently in the dispatcher
// buffer is at or above the pause threshold, this method sleeps until the
// buffer drains below the resume threshold (hysteresis: pause at 100k, resume
// at 80k). The current in-flight diff batch always completes and pushes freely.
//
// This keeps the diff stage from generating work faster than the dispatch
// stage can deliver it, without ever dropping a single nonce.
//
// Returns ctx.Err() if the context is cancelled while waiting.
//
// Time: O(1) amortised — only blocks when buffer ≥ pauseThreshold.
// Space: O(1) — one watcher goroutine exists only for the duration of a pause.
func (d *AccountDispatcher) WaitForBatchStart(ctx context.Context) error {
	pauseThreshold := d.cfg.NonceBufferPauseThreshold

	// Fast path: buffer is well below the threshold — proceed immediately.
	if d.nonceBufferCount.Load() < pauseThreshold {
		return nil
	}

	// Slow path: launch a watcher that broadcasts on context cancellation so
	// the goroutine blocked in pauseCond.Wait can observe ctx.Err() and return.
	// sync.Cond has no native context support; this goroutine bridges that gap.
	stopWatcher := make(chan struct{})
	defer close(stopWatcher) // always fires on return, ensuring no goroutine leak

	go func() {
		select {
		case <-ctx.Done():
			d.pauseCond.Broadcast()
		case <-stopWatcher:
		}
	}()

	d.pauseMu.Lock()
	for d.nonceBufferCount.Load() >= pauseThreshold && ctx.Err() == nil {
		// Wait atomically releases pauseMu and suspends this goroutine.
		// It reacquires pauseMu before returning (standard sync.Cond contract).
		// Woken by: workers via broadcastResume (when count drops below 80% threshold),
		// or the watcher goroutine above when ctx is cancelled.
		d.pauseCond.Wait()
	}
	d.pauseMu.Unlock()

	return ctx.Err()
}

// EnqueueNonces pushes one nonce page into the dispatcher buffer.
//
// The buffer always accepts data. EnqueueNonces is non-blocking in all
// realistic scenarios because channel capacity is sized to absorb the
// worst-case overshoot from all diff parents completing simultaneously
// when the pause threshold fires (see NewAccountDispatcher for the formula).
//
// The only reason EnqueueNonces can return an error is context cancellation.
// This should be called AFTER WaitForBatchStart to respect flow control, but
// that is a soft contract — the buffer guarantees acceptance regardless.
//
// Time: O(1). Space: O(1).
func (d *AccountDispatcher) EnqueueNonces(ctx context.Context, nonces []uint64) error {
	if len(nonces) == 0 {
		return nil
	}

	page := noncePage{
		Nonces:    nonces,
		Retries:   0,
		PageIndex: 0, // 1-based index assigned by the worker on first dispatch
	}

	// Increment inflight BEFORE the channel send.
	// If we incremented after, there is a window where the page is in the
	// channel but inflight==0, which would let tryCloseDone fire prematurely
	// and close `done` before this page is ever processed.
	d.inflight.Add(1)

	select {
	case d.nonceChan <- page:
		d.nonceBufferCount.Add(int64(len(nonces)))
		return nil
	case <-ctx.Done():
		// Send did not happen — undo the inflight increment and check shutdown.
		d.inflight.Add(-1)
		d.tryCloseDone()
		return fmt.Errorf("dispatcher: enqueue cancelled: %w", ctx.Err())
	}
}

// CloseInput signals that the producer has finished pushing nonce pages.
// Workers continue draining the channel until empty, then Run returns.
//
// CloseInput is idempotent — safe to call more than once.
// After CloseInput, EnqueueNonces must not be called.
//
// Time: O(1). Space: O(1).
func (d *AccountDispatcher) CloseInput() {
	d.inputClosed.Store(true)
	d.tryCloseDone()
	// Wake any diff goroutine blocked in WaitForBatchStart so it can observe
	// the closed state via ctx.Err() on its next check.
	d.pauseCond.Broadcast()
}

// ─── Internal helpers (called by workers) ────────────────────────────────────

// broadcastResume is called by dispatch workers after decrementing
// nonceBufferCount. If the count has dropped below the resume threshold,
// it wakes all diff goroutines blocked in WaitForBatchStart.
//
// Acquires pauseMu before broadcasting — required so the Broadcast and the
// producer's cond.Wait are serialised under the same lock (standard sync.Cond
// invariant: condition changes must be made under the lock).
//
// Time: O(1). Space: O(1).
func (d *AccountDispatcher) broadcastResume(currentCount int64) {
	resumeThreshold := d.cfg.NonceBufferPauseThreshold *
		int64(d.cfg.NonceBufferResumePct) / 100

	if currentCount < resumeThreshold {
		d.pauseMu.Lock()
		d.pauseCond.Broadcast()
		d.pauseMu.Unlock()
	}
}

// tryCloseDone closes the `done` channel exactly once when both conditions hold:
//  1. CloseInput has been called (no more pages will be admitted).
//  2. inflight == 0 (every admitted page has been finalised).
//
// Workers call this after finalising each page (delivered or dead-lettered).
// CloseInput calls it after marking input closed.
// Using sync.Once prevents a double-close panic.
//
// Time: O(1). Space: O(1).
func (d *AccountDispatcher) tryCloseDone() {
	if d.inputClosed.Load() && d.inflight.Load() == 0 {
		d.doneOnce.Do(func() { close(d.done) })
	}
}

// recordDeadLetter appends a permanently failed page to the dead-letter list
// and fires the OnDeadLetter callback if one is registered.
//
// Time: O(1) amortised. Space: O(1) per call.
func (d *AccountDispatcher) recordDeadLetter(ctx context.Context, page noncePage, hint string) {
	dead := types.DeadLetterPage{
		Nonces:      page.Nonces,
		RetryCount:  page.Retries,
		FailureHint: hint,
	}

	d.deadMu.Lock()
	d.deadLetters = append(d.deadLetters, dead)
	d.deadMu.Unlock()

	if d.callbacks.OnDeadLetter != nil {
		d.callbacks.OnDeadLetter(ctx, dead)
	}
}
