package accounts

import (
	"context"
	"fmt"
	"maps"
	"sync"

	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
	"github.com/JupiterMetaLabs/goroutine-orchestrator/manager/local"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	GROinternal "github.com/JupiterMetaLabs/JMDN-FastSync/internal/GRO"

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
	//
	// Use scaled comparison (clientTotalKeys*100 < serverTotal*pct) instead of
	// (clientTotalKeys < serverTotal*pct/100) to avoid integer truncation when
	// serverTotal < 17, where serverTotal*6/100 = 0 in integer arithmetic and
	// the threshold becomes unreachable (uint64 can never be < 0).
	if clientTotalKeys*100 < serverTotal*constants.FullSyncThresholdPct {
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

// Full-sync path 

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

//  Diff path 
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
	//  GRO setup
	// Use the process-wide singleton loaded by GRO.EagerLoading() at startup.
	// Creating a new AppManager per call would leak manager objects in long-lived
	// processes; the singleton avoids that and aligns with internal/GRO's pattern.
	appGRO := GROinternal.GetApp(GROinternal.AccountsSyncApp)
	if appGRO == nil {
		return nil, fmt.Errorf("gro app %q not initialised — call GRO.EagerLoading() at startup", GROinternal.AccountsSyncApp)
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

	//  Work channel 
	// Capacity = actualParents so the producer can fill one batch per parent
	// before any parent has finished processing, avoiding producer stalls.
	workChan := make(chan []*types.Account, actualParents)

	//  Producer 
	// Sequential iterator → workChan.  Batches are non-overlapping by construction.
	// Iterator errors are forwarded via producerErr (buffered=1, written at most once)
	// so a truncated result set is never silently returned to the caller.
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

	//  Parent result slots 
	// Each parent writes exclusively to parentMaps[parentIdx] and parentErrs[parentIdx];
	// no mutex needed — slot ownership is guaranteed by the captured parentIdx.
	parentMaps := make([]map[uint64]*types.Account, actualParents)
	parentErrs := make([]error, actualParents)

	//  Spawn up to NumDiffParents parent goroutines (clamped to server total) 
	for i := 0; i < actualParents; i++ {
		parentIdx := i // capture loop variable for closure

		spawnErr := localMgr.Go(
			fmt.Sprintf("%s-%02d", constants.ParentWGName, parentIdx),
			func(pctx context.Context) error {
				localMap := make(map[uint64]*types.Account)

				for {
					select {
					case <-pctx.Done():
						// Context cancelled mid-flight; commit what we have and
						// record the error so diffWithGRO can surface it after wg.Wait().
						parentMaps[parentIdx] = localMap
						parentErrs[parentIdx] = pctx.Err()
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

	// Block until all parents have committed their localMaps.
	wg.Wait()

	//  Check producer error 
	// Non-blocking read: the channel is buffered(1) and written at most once.
	select {
	case err := <-producerErr:
		return nil, fmt.Errorf("iterator: %w", err)
	default:
	}

	//  Check parent errors 
	// Surface the first context-cancellation error from any parent goroutine.
	for i, perr := range parentErrs {
		if perr != nil {
			return nil, fmt.Errorf("parent %d: %w", i, perr)
		}
	}

	//  Final merge 
	// All parents have returned; sequential merge is safe.
	final := make(map[uint64]*types.Account)
	for _, pm := range parentMaps {
		maps.Copy(final, pm)
	}
	return final, nil
}

//  Child-level processing 
// processWithChildren divides a parent's in-memory batch (≤parentMemoryWindow accounts)
// into numDiffChildren equal slices (≤childWindow accounts each) and checks every
// account nonce against the SwappableART using native goroutines.
//
// SwappableART.Contains is goroutine-safe (internal sync.RWMutex with RLock), so
// concurrent child reads proceed fully in parallel without serialization.
// Each child builds its own local map before merging into the shared result —
// the mutex is held only for the final map merge, not during the ART lookup loop.
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
