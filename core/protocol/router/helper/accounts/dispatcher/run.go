package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// Run starts cfg.DispatchWorkers goroutines and blocks until all admitted
// nonce pages have been delivered or dead-lettered, or until ctx is cancelled.
//
// Typical call pattern (caller drives the diff stage in parallel):
//
//	summaryCh := make(chan types.DispatchSummary, 1)
//	go func() {
//	    summary, err := d.Run(ctx)
//	    ...
//	    summaryCh <- summary
//	}()
//	// diff stage calls WaitForBatchStart + EnqueueNonces in a loop
//	d.CloseInput()
//	summary := <-summaryCh
//
// Returns a partial DispatchSummary plus ctx.Err() on context cancellation —
// whatever was delivered before cancellation is captured in the summary.
//
// Time:  O(total_nonces) — one DB fetch + one network round-trip per page.
// Space: O(DispatchWorkers × NoncePageSize) for concurrent in-flight pages.
func (d *AccountDispatcher) Run(ctx context.Context) (types.DispatchSummary, error) {
	var wg sync.WaitGroup
	for range d.cfg.DispatchWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.dispatchWorker(ctx)
		}()
	}

	var runErr error
	select {
	case <-d.done:
		// Normal completion: all pages delivered or dead-lettered.
	case <-ctx.Done():
		// Session cancelled — workers will see ctx.Done() on their next loop.
		runErr = ctx.Err()
	}

	// Always wait: wg.Wait() ensures every worker has fully exited before we
	// read the accumulators. Without this, a worker that fired tryCloseDone
	// may still be mid-return when we read deliveredPages.
	wg.Wait()

	d.deadMu.Lock()
	deadLetters := make([]types.DeadLetterPage, len(d.deadLetters))
	copy(deadLetters, d.deadLetters)
	d.deadMu.Unlock()

	var permanentFailedAccounts uint64
	for _, dl := range deadLetters {
		permanentFailedAccounts += uint64(len(dl.Nonces))
	}

	summary := types.DispatchSummary{
		DeliveredPages:          d.deliveredPages.Load(),
		DeliveredAccounts:       d.deliveredAccounts.Load(),
		PermanentFailedPages:    uint32(len(deadLetters)),
		PermanentFailedAccounts: permanentFailedAccounts,
		DeadLetters:             deadLetters,
	}
	return summary, runErr
}

// dispatchWorker is the main loop for one dispatch goroutine.
//
// It waits for pages on nonceChan and processes each one. It exits when:
//   - d.done is closed (all pages finalised, normal shutdown), OR
//   - ctx is cancelled (session-level cancellation)
//
// Workers never close nonceChan — they only read from it.
//
// Time:  O(1) per iteration overhead.
// Space: O(NoncePageSize) per active page (one DB result set at a time).
func (d *AccountDispatcher) dispatchWorker(ctx context.Context) {
	for {
		select {
		case <-d.done:
			// All pages are finalised — nothing left to process.
			return
		case <-ctx.Done():
			// Session cancelled — exit immediately.
			return
		case page := <-d.nonceChan:
			d.processPage(ctx, page)
		}
	}
}

// processPage handles the full lifecycle of one nonce page:
//
//  1. Decrement nonceBufferCount (page left the channel) → signal resume to diff stage
//  2. Assign a 1-based page index if this is the first delivery attempt
//  3. Fetch full account records from the DB via FetchAccounts callback
//  4. Deliver to client via SendPage callback (with DispatchACKTimeout deadline)
//  5. On success: record delivery, finalise page
//  6. On failure: re-enqueue if retries remain; dead-letter if exhausted
//
// Time:  O(n) where n = len(page.Nonces) — dominated by DB fetch.
// Space: O(n) — one []*Account slice per call, freed after delivery.
func (d *AccountDispatcher) processPage(ctx context.Context, page noncePage) {
	// Step 1: page has left the channel — decrement buffer count and
	// potentially wake paused diff goroutines (hysteresis resume signal).
	newCount := d.nonceBufferCount.Add(-int64(len(page.Nonces)))
	d.broadcastResume(newCount)

	// Step 2: assign page index on first attempt.
	if page.PageIndex == 0 {
		page.PageIndex = d.pageSequence.Add(1) // 1-based, monotonically increasing
	}

	// Step 3: fetch full account records for this nonce set.
	dbStart := time.Now()
	accounts, fetchErr := d.callbacks.FetchAccounts(ctx, page.Nonces)
	dbMs := time.Since(dbStart).Milliseconds()

	if fetchErr != nil {
		d.handleFailure(ctx, page, fetchErr,
			types.DispatchPageMetrics{
				PageIndex:  page.PageIndex,
				NonceCount: len(page.Nonces),
				DBFetchMS:  dbMs,
				Retries:    page.Retries,
				Success:    false,
			})
		return
	}

	// Step 4: deliver to client with a per-page ACK deadline.
	sendCtx, sendCancel := context.WithTimeout(ctx, d.cfg.DispatchACKTimeout)
	defer sendCancel()

	sendStart := time.Now()
	sendErr := d.callbacks.SendPage(sendCtx, page.PageIndex, accounts)
	sendMs := time.Since(sendStart).Milliseconds()

	metrics := types.DispatchPageMetrics{
		PageIndex:  page.PageIndex,
		NonceCount: len(page.Nonces),
		DBFetchMS:  dbMs,
		SendMS:     sendMs,
		Retries:    page.Retries,
		Success:    sendErr == nil,
	}

	if sendErr != nil {
		d.handleFailure(ctx, page, sendErr, metrics)
		return
	}

	// Step 5: success — record delivery and finalise.
	d.deliveredPages.Add(1)
	d.deliveredAccounts.Add(uint64(len(accounts)))

	if d.callbacks.OnPageMetrics != nil {
		d.callbacks.OnPageMetrics(ctx, metrics)
	}

	d.inflight.Add(-1)
	d.tryCloseDone()
}

// handleFailure decides whether to re-queue a failed page or dead-letter it.
//
// Re-queue path (retries < DispatchMaxRetries):
//   - Increments Retries on the page, puts it back on nonceChan.
//   - Restores nonceBufferCount (page is back in the buffer).
//   - inflight is NOT changed — the page is still in flight.
//   - If the channel send is blocked by ctx cancellation, falls through to dead-letter.
//
// Dead-letter path (retries exhausted or ctx cancelled during re-queue):
//   - Appends to the dead-letter list, fires OnDeadLetter callback.
//   - Decrements inflight and calls tryCloseDone.
//
// Time: O(1). Space: O(1).
func (d *AccountDispatcher) handleFailure(ctx context.Context, page noncePage, err error, metrics types.DispatchPageMetrics) {
	if d.callbacks.OnPageMetrics != nil {
		d.callbacks.OnPageMetrics(ctx, metrics)
	}

	if page.Retries < d.cfg.DispatchMaxRetries {
		page.Retries++
		// Try to re-enqueue. The buffer always has capacity (see NewAccountDispatcher
		// channel capacity formula), but ctx cancellation is a possible exit.
		select {
		case d.nonceChan <- page:
			// Restore the buffer count — page is back in the channel.
			d.nonceBufferCount.Add(int64(len(page.Nonces)))
			// inflight unchanged: page is still in flight (just re-queued).
			return
		case <-ctx.Done():
			// Context cancelled while re-queuing — fall through to dead-letter.
		}
	}

	// Dead-letter: page exhausted retries or could not be re-queued.
	hint := fmt.Sprintf("after %d retries: %v", page.Retries, err)
	d.recordDeadLetter(ctx, page, hint)
	d.inflight.Add(-1)
	d.tryCloseDone()
}
