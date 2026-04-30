package accounts

import (
	"context"
	"fmt"
	"sync"

	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
	"github.com/JupiterMetaLabs/goroutine-orchestrator/manager/local"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper/accounts/dispatcher"
	GROinternal "github.com/JupiterMetaLabs/JMDN-FastSync/internal/GRO"
)

// ComputeAccountDiff identifies every account the client is missing and streams
// them to the client via the provided dispatcher.
//
// The dispatcher and the diff computation run concurrently — pages are enqueued
// as missing nonces are found, and dispatch workers deliver them while the diff
// is still running. The caller must start dispatcher.Run in a goroutine BEFORE
// calling ComputeAccountDiff.
//
// ComputeAccountDiff calls dispatcher.CloseInput() when all nonces have been
// enqueued. The caller waits for dispatcher.Run to return for the final summary.
//
// Full-sync fast path (IsFullSync=true):
//
//	If clientTotalKeys < 6 % of the server's total, every server account nonce
//	is pushed to the dispatcher without consulting the ART. Cheaper than
//	diff when the client is far behind.
//
// Diff path (IsFullSync=false):
//
//	30-parent × 10-child GRO pool. Each parent pauses at WaitForBatchStart
//	before pulling the next batch — the dispatcher's soft pause threshold
//	(100k nonces) signals when to slow down. Pages of NoncePageSize nonces
//	are pushed as they are found; the final page may be smaller.
//
// Time:  O(s) where s = total server accounts — one ART lookup per account.
// Space: O(w) where w = ParentMemoryWindow × NumDiffParents — accounts live
//        only for the duration of one parent's batch.
func ComputeAccountDiff(
	ctx context.Context,
	swappable *art.SwappableART,
	blockInfo types.BlockInfo,
	clientTotalKeys uint64,
	d *dispatcher.AccountDispatcher,
) (isFullSync bool, err error) {
	iter := blockInfo.NewAccountManager().NewAccountNonceIterator(constants.ParentMemoryWindow)
	defer iter.Close()

	serverTotal, err := iter.TotalAccounts()
	if err != nil {
		return false, fmt.Errorf("accounts diff: server total: %w", err)
	}

	// Full-sync fast path: client is more than (100−fullSyncThresholdPct)% behind.
	if clientTotalKeys*100 < serverTotal*constants.FullSyncThresholdPct {
		if err := collectAll(ctx, iter, d); err != nil {
			return false, fmt.Errorf("accounts diff: full sync collect: %w", err)
		}
		d.CloseInput()
		return true, nil
	}

	// Diff path.
	if err := diffWithGRO(ctx, swappable, iter, serverTotal, d); err != nil {
		return false, fmt.Errorf("accounts diff: diff computation: %w", err)
	}
	d.CloseInput()
	return false, nil
}

// ─── Full-sync path ───────────────────────────────────────────────────────────

// collectAll drains the iterator and pushes every nonce to the dispatcher in
// pages of NoncePageSize. The final page may be smaller.
// Used when the client is too far behind for a diff to be worthwhile.
//
// The dispatcher's WaitForBatchStart gate is respected between iterator batches
// so the full-sync path honours the same flow-control contract as the diff path.
//
// Time: O(s) where s = total server accounts. Space: O(NoncePageSize) — one page
//       buffer at a time; previous page is GC-eligible after EnqueueNonces returns.
func collectAll(
	ctx context.Context,
	iter types.AccountNonceIterator,
	d *dispatcher.AccountDispatcher,
) error {
	var pending []uint64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Pause gate: obey the dispatcher's soft limit before fetching the next batch.
		if err := d.WaitForBatchStart(ctx); err != nil {
			return err
		}

		batch, err := iter.NextBatch()
		if err != nil {
			return fmt.Errorf("iterator: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		for _, acc := range batch {
			pending = append(pending, acc.Nonce)
		}

		// Flush all full pages accumulated so far.
		for len(pending) >= constants.NoncePageSize {
			if err := d.EnqueueNonces(ctx, pending[:constants.NoncePageSize]); err != nil {
				return fmt.Errorf("enqueue full-sync page: %w", err)
			}
			pending = pending[constants.NoncePageSize:]
		}
	}

	// Flush the final partial page (may be any size 1..NoncePageSize−1).
	if len(pending) > 0 {
		if err := d.EnqueueNonces(ctx, pending); err != nil {
			return fmt.Errorf("enqueue full-sync final page: %w", err)
		}
	}
	return nil
}

// ─── Diff path ────────────────────────────────────────────────────────────────

// diffWithGRO runs the 30-parent × 10-child diff computation and streams missing
// nonces to the dispatcher concurrently with the delivery workers.
//
// Dataflow:
//
//	producer goroutine
//	  └─ NextBatch() → workChan (capacity=numDiffParents)
//	       ↓  batch of ≤ParentMemoryWindow *Account
//	  30 parent goroutines (GRO-tracked)
//	       │  WaitForBatchStart ← pause gate (honours 100k soft limit)
//	       ↓  pull batch from workChan
//	       │  split into NumDiffChildren slices → 10 child goroutines
//	       │  SwappableART.Contains(nonce) — concurrent reads (RLock safe)
//	       ↓  collect missing nonces → local []uint64
//	       │  flush full NoncePageSize pages → dispatcher.EnqueueNonces
//	  after workChan closed: flush remaining nonces (final partial page)
//
// Time:  O(s) — one ART Contains per server account.
// Space: O(ParentMemoryWindow) per parent — accounts freed after each batch.
func diffWithGRO(
	ctx context.Context,
	swappable *art.SwappableART,
	iter types.AccountNonceIterator,
	serverTotal uint64,
	d *dispatcher.AccountDispatcher,
) error {
	appGRO := GROinternal.GetApp(GROinternal.AccountsSyncApp)
	if appGRO == nil {
		return fmt.Errorf("gro app %q not initialised — call GRO.EagerLoading() at startup", GROinternal.AccountsSyncApp)
	}
	localMgr := local.NewLocalManager(constants.GroApp, constants.GroLocal)
	if _, err := localMgr.CreateLocal(constants.GroLocal); err != nil {
		return fmt.Errorf("gro create local: %w", err)
	}

	wg, err := localMgr.NewFunctionWaitGroup(ctx, constants.ParentWGName)
	if err != nil {
		return fmt.Errorf("gro wait group: %w", err)
	}

	actualParents := min(constants.NumDiffParents, int(serverTotal))
	if actualParents == 0 {
		return nil
	}

	workChan := make(chan []*types.Account, actualParents)

	producerErr := make(chan error, 1)
	go func() {
		defer close(workChan)
		for {
			batch, err := iter.NextBatch()
			if err != nil {
				producerErr <- err
				return
			}
			if len(batch) == 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case workChan <- batch:
			}
		}
	}()

	parentErrs := make([]error, actualParents)

	for i := 0; i < actualParents; i++ {
		parentIdx := i

		spawnErr := localMgr.Go(
			fmt.Sprintf("%s-%02d", constants.ParentWGName, parentIdx),
			func(pctx context.Context) error {
				var localNonces []uint64

				for {
					// Pause gate: check dispatcher buffer before pulling the next batch.
					// If the buffer is at/above the soft limit, this sleeps until it
					// drains below 80 % — the current batch always finishes and pushes.
					if err := d.WaitForBatchStart(pctx); err != nil {
						// Flush whatever we have before returning so no nonces are lost.
						if len(localNonces) > 0 {
							_ = d.EnqueueNonces(pctx, localNonces)
						}
						parentErrs[parentIdx] = err
						return err
					}

					select {
					case <-pctx.Done():
						if len(localNonces) > 0 {
							_ = d.EnqueueNonces(pctx, localNonces)
						}
						parentErrs[parentIdx] = pctx.Err()
						return pctx.Err()

					case batch, ok := <-workChan:
						if !ok {
							// workChan closed — flush remaining nonces as the final page.
							if len(localNonces) > 0 {
								if err := d.EnqueueNonces(pctx, localNonces); err != nil {
									parentErrs[parentIdx] = err
									return err
								}
							}
							return nil
						}

						// Find nonces in this batch that are missing from the client ART.
						missing := processWithChildren(swappable, batch)
						localNonces = append(localNonces, missing...)

						// Flush full pages immediately — don't wait until the batch ends.
						for len(localNonces) >= constants.NoncePageSize {
							if err := d.EnqueueNonces(pctx, localNonces[:constants.NoncePageSize]); err != nil {
								parentErrs[parentIdx] = err
								return err
							}
							localNonces = localNonces[constants.NoncePageSize:]
						}
					}
				}
			},
			local.AddToWaitGroup(constants.ParentWGName),
		)

		if spawnErr != nil {
			go func() {
				for range workChan {
				}
			}()
			return fmt.Errorf("spawn parent %d: %w", parentIdx, spawnErr)
		}
	}

	wg.Wait()

	select {
	case err := <-producerErr:
		return fmt.Errorf("iterator: %w", err)
	default:
	}

	for i, perr := range parentErrs {
		if perr != nil {
			return fmt.Errorf("parent %d: %w", i, perr)
		}
	}

	return nil
}

// ─── Child-level processing ───────────────────────────────────────────────────

// processWithChildren divides a parent batch into NumDiffChildren slices and
// checks every account nonce against the SwappableART concurrently.
//
// Returns a []uint64 of nonces absent from the client's ART. The slice is
// unordered — ordering is not required for AccountSync page delivery.
//
// SwappableART.Contains is goroutine-safe (internal RWMutex with RLock) so
// concurrent child reads proceed fully in parallel without serialisation.
// Each child builds its own local slice before appending to the shared result;
// the mutex is held only for the brief append, not during ART lookups.
//
// Time:  O(n) where n = len(batch) — one Contains per account.
// Space: O(m) where m = number of missing accounts in this batch.
func processWithChildren(
	swappable *art.SwappableART,
	batch []*types.Account,
) []uint64 {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		result []uint64
	)

	actualChildren := min(constants.NumDiffChildren, len(batch))

	for i := range actualChildren {
		start := i * constants.ChildWindow
		if start >= len(batch) {
			break
		}
		slice := batch[start:min(start+constants.ChildWindow, len(batch))]

		wg.Add(1)
		go func(slice []*types.Account) {
			defer wg.Done()

			// Phase 1: ART lookups — no shared lock, fully parallel.
			var childNonces []uint64
			for _, acc := range slice {
				if !swappable.Contains(acc.Nonce) {
					childNonces = append(childNonces, acc.Nonce)
				}
			}

			// Phase 2: append to shared result — lock only for this brief operation.
			if len(childNonces) == 0 {
				return
			}
			mu.Lock()
			result = append(result, childNonces...)
			mu.Unlock()
		}(slice)
	}

	wg.Wait()
	return result
}
