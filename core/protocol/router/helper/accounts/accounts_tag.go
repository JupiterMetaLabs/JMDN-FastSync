package accounts

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/JupiterMetaLabs/goroutine-orchestrator/manager/app"
	"github.com/JupiterMetaLabs/goroutine-orchestrator/manager/local"
	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// AccountDiff is the output of ComputeAccountDiff.
type AccountDiff struct {
	// Missing maps nonce → full Account record for every server account the client lacks.
	Missing map[uint64]*types.Account

	// IsFullSync is true when the client had fewer than fullSyncThresholdPct % of the
	// server's accounts; in that case the ART was not consulted and all server accounts
	// are included in Missing.
	IsFullSync bool
}

// ComputeAccountDiff identifies every account the client is missing.
//
// Full-sync fast path (IsFullSync=true):
//
//	If clientTotalKeys < 6 % of the server's total, every server account is returned
//	without querying the ART.  This avoids the overhead of building a diff when the
//	client is far behind — it is cheaper to transfer everything.
//
// Diff path (IsFullSync=false):
//
//	A two-level goroutine pool (30 parents × 10 children) reads the server account
//	iterator in bounded memory windows and performs concurrent reads against the
//	immutable SwappableART supplied by the client upload phase.
//
//	Memory ceiling: 30 parents × 10 000 accounts = 300 000 live at any instant.
func ComputeAccountDiff(
	ctx context.Context,
	swappable *art.SwappableART,
	blockInfo types.BlockInfo,
	clientTotalKeys uint64,
) (*AccountDiff, error) {
	iter := blockInfo.NewAccountNonceIterator(constants.ParentMemoryWindow)
	defer iter.Close()

	serverTotal, err := iter.TotalAccounts()
	if err != nil {
		return nil, fmt.Errorf("accounts diff: server total: %w", err)
	}

	// Full-sync fast path: client is more than (100-fullSyncThresholdPct)% behind.
	if clientTotalKeys < serverTotal*constants.FullSyncThresholdPct/100 {
		missing, err := collectAll(ctx, iter)
		if err != nil {
			return nil, fmt.Errorf("accounts diff: full sync collect: %w", err)
		}
		return &AccountDiff{Missing: missing, IsFullSync: true}, nil
	}

	// Diff path: concurrent 30-parent × 10-child ART lookup.
	missing, err := diffWithGRO(ctx, swappable, iter, serverTotal)
	if err != nil {
		return nil, fmt.Errorf("accounts diff: diff computation: %w", err)
	}
	return &AccountDiff{Missing: missing, IsFullSync: false}, nil
}

// ── Full-sync path ─────────────────────────────────────────────────────────────

// collectAll drains the iterator and returns every account.
// Context cancellation is checked between batches.
func collectAll(ctx context.Context, iter types.AccountNonceIterator) (map[uint64]*types.Account, error) {
	result := make(map[uint64]*types.Account)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		batch, err := iter.NextBatch()
		if err != nil {
			return nil, fmt.Errorf("iterator: %w", err)
		}
		if len(batch) == 0 {
			return result, nil
		}
		for _, acc := range batch {
			result[acc.Nonce] = acc
		}
	}
}

// ── Diff path ──────────────────────────────────────────────────────────────────

// diffWithGRO runs the 30-parent × 10-child diff computation using the GRO framework
// for parent-goroutine lifecycle management.
//
// Dataflow:
//
//	producer goroutine
//	  └─ NextBatch() → workChan (capacity=numDiffParents)
//	       ↓  batch of ≤parentMemoryWindow *Account
//	  30 parent goroutines (GRO-tracked, AddToWaitGroup)
//	       ↓  split into numDiffChildren slices of childWindow each
//	  10 child goroutines per parent (native go + sync.WaitGroup)
//	       └─ SwappableART.Contains(nonce) — goroutine-safe concurrent reads
//	       └─ per-child hashset {nonce → *Account} for accounts NOT in ART
//	       → merged into parent hashset
//	  30 parent hashsets → final sequential merge → returned map
//
// No-duplicate guarantee: the sequential iterator makes every batch non-overlapping;
// the buffered workChan ensures each batch is consumed by exactly one parent.
func diffWithGRO(
	ctx context.Context,
	swappable *art.SwappableART,
	iter types.AccountNonceIterator,
	serverTotal uint64,
) (map[uint64]*types.Account, error) {
	// ── GRO setup ──────────────────────────────────────────────────────────────
	// CreateApp / CreateLocal are idempotent — safe to call on repeated invocations.
	appMgr := app.NewAppManager(constants.GroApp)
	if _, err := appMgr.CreateApp(); err != nil {
		return nil, fmt.Errorf("gro create app: %w", err)
	}
	localMgr := local.NewLocalManager(constants.GroApp, constants.GroLocal)
	if _, err := localMgr.CreateLocal(constants.GroLocal); err != nil {
		return nil, fmt.Errorf("gro create local: %w", err)
	}

	// Pre-create the function WaitGroup so AddToWaitGroup can find it immediately.
	// wg.Add(1) is called inside Go() (before goroutine launch) and wg.Done() on exit.
	wg, err := localMgr.NewFunctionWaitGroup(ctx, constants.ParentWGName)
	if err != nil {
		return nil, fmt.Errorf("gro wait group: %w", err)
	}
	actualParents := min(constants.NumDiffParents, int(serverTotal))
	if actualParents == 0 {
		return make(map[uint64]*types.Account), nil
	}

	// ── Work channel ───────────────────────────────────────────────────────────
	// Capacity = actualParents so the producer can fill one batch per parent
	// before any parent has finished processing, avoiding producer stalls.
	workChan := make(chan []*types.Account, actualParents)

	// ── Producer ───────────────────────────────────────────────────────────────
	// Sequential iterator → workChan.  Batches are non-overlapping by construction.
	go func() {
		defer close(workChan)
		for {
			batch, err := iter.NextBatch()
			if err != nil || len(batch) == 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case workChan <- batch:
			}
		}
	}()

	// ── Parent result slots ────────────────────────────────────────────────────
	// Each parent writes exclusively to parentMaps[parentIdx]; no mutex needed here.
	parentMaps := make([]map[uint64]*types.Account, actualParents)

	// ── Spawn up to NumDiffParents parent goroutines (clamped to server total) ─
	for i := 0; i < actualParents; i++ {
		parentIdx := i // capture loop variable for closure

		spawnErr := localMgr.Go(
			fmt.Sprintf("%s-%02d", constants.ParentWGName, parentIdx),
			func(pctx context.Context) error {
				localMap := make(map[uint64]*types.Account)

				for {
					select {
					case <-pctx.Done():
						// Context cancelled mid-flight; commit what we have.
						parentMaps[parentIdx] = localMap
						return pctx.Err()

					case batch, ok := <-workChan:
						if !ok {
							// Producer finished; no more batches for any parent.
							parentMaps[parentIdx] = localMap
							return nil
						}
						// Push this parentMemoryWindow-sized batch through
						// numDiffChildren child goroutines and merge results.
						childResult := processWithChildren(swappable, batch)
						maps.Copy(localMap, childResult)
					}
				}
			},
			local.AddToWaitGroup(constants.ParentWGName),
		)
		if spawnErr != nil {
			// Drain the work channel in the background so the producer goroutine
			// is not blocked forever on a full channel after we return the error.
			go func() {
				for range workChan {
				}
			}()
			return nil, fmt.Errorf("spawn parent %d: %w", parentIdx, spawnErr)
		}
	}

	// Block until all 30 parents have committed their localMaps.
	wg.Wait()

	// ── Final merge ────────────────────────────────────────────────────────────
	// All parents have returned; sequential merge is safe.
	final := make(map[uint64]*types.Account)
	for _, pm := range parentMaps {
		maps.Copy(final, pm)
	}
	return final, nil
}

// ── Child-level processing ─────────────────────────────────────────────────────

// processWithChildren divides a parent's in-memory batch (≤parentMemoryWindow accounts)
// into numDiffChildren equal slices (≤childWindow accounts each) and checks every
// account nonce against the SwappableART using native goroutines.
//
// SwappableART.Contains is goroutine-safe (internal sync.Mutex), so concurrent child
// reads require no additional locking.  Each child builds its own local map before
// merging into the shared result — the lock is held only for the final map merge,
// not during the ART lookup loop.
//
// Returns: merged map of {nonce → *Account} for accounts absent from the client's ART.
func processWithChildren(
	swappable *art.SwappableART,
	batch []*types.Account,
) map[uint64]*types.Account {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		merged = make(map[uint64]*types.Account)
	)

	// Don't spawn more children than there are accounts in this batch.
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
			childMap := make(map[uint64]*types.Account, len(slice))
			for _, acc := range slice {
				if !swappable.Contains(acc.Nonce) {
					childMap[acc.Nonce] = acc
				}
			}

			// Phase 2: merge into parent result — lock only for this brief copy.
			if len(childMap) == 0 {
				return
			}
			mu.Lock()
			maps.Copy(merged, childMap)
			mu.Unlock()
		}(slice)
	}

	wg.Wait()
	return merged
}
