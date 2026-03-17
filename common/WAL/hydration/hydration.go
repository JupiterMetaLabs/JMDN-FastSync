package hydration

import (
	"fmt"

	walPkg "github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
)

// PhaseState holds the last committed WAL event for every sync phase.
// A nil field means no entry of that phase exists in the current WAL window
// (either a fresh node or all entries have been pruned past the last checkpoint).
type PhaseState struct {
	PriorSync      *walPkg.PriorSyncEvent
	HeaderSync     *walPkg.HeaderSyncEvent
	DataSync       *walPkg.DataSyncEvent
	Reconciliation *walPkg.ReconciliationBatchEvent
}

// Hydrator replays WAL entries on startup to reconstruct the last committed
// state for each sync phase. Because CreateCheckpoint is called after every
// successful DB commit, every entry still present in the WAL corresponds to
// committed data — hydration is purely for state reconstruction, not for
// re-applying uncommitted writes.
type Hydrator struct {
	wal *walPkg.WAL
}

// NewHydrator creates a Hydrator backed by the provided WAL instance.
func NewHydrator(w *walPkg.WAL) *Hydrator {
	return &Hydrator{wal: w}
}

// HydrateAll performs a single pass through the WAL and returns the last
// committed event for every sync phase. Prefer this over calling individual
// Hydrate* methods when you need all phases at startup — it avoids N separate
// scans of the same log.
//
// Returns a non-nil *PhaseState even when the WAL is empty; individual fields
// will be nil for phases with no WAL history.
func (h *Hydrator) HydrateAll() (*PhaseState, error) {
	state := &PhaseState{}

	err := h.wal.ReplayEvents(func(entry walPkg.WALEntry) error {
		switch entry.Type {
		case wal_types.PriorSync:
			ev := &walPkg.PriorSyncEvent{}
			if err := ev.Deserialize(entry.Data); err != nil {
				return fmt.Errorf("PriorSync deserialize at LSN %d: %w", entry.LSN, err)
			}
			state.PriorSync = ev

		case wal_types.HeaderSync:
			ev := &walPkg.HeaderSyncEvent{}
			if err := ev.Deserialize(entry.Data); err != nil {
				return fmt.Errorf("HeaderSync deserialize at LSN %d: %w", entry.LSN, err)
			}
			state.HeaderSync = ev

		case wal_types.DataSync:
			ev := &walPkg.DataSyncEvent{}
			if err := ev.Deserialize(entry.Data); err != nil {
				return fmt.Errorf("DataSync deserialize at LSN %d: %w", entry.LSN, err)
			}
			state.DataSync = ev

		case wal_types.Reconciliation:
			ev := &walPkg.ReconciliationBatchEvent{}
			if err := ev.Deserialize(entry.Data); err != nil {
				return fmt.Errorf("Reconciliation deserialize at LSN %d: %w", entry.LSN, err)
			}
			state.Reconciliation = ev
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("WAL hydration failed: %w", err)
	}

	return state, nil
}

// HydratePriorSync returns the last PriorSync WAL entry.
// Returns nil without error when no PriorSync entries exist.
func (h *Hydrator) HydratePriorSync() (*walPkg.PriorSyncEvent, error) {
	return hydrateLastEntry[walPkg.PriorSyncEvent](h.wal, wal_types.PriorSync)
}

// HydrateHeaderSync returns the last HeaderSync WAL entry.
// Returns nil without error when no HeaderSync entries exist.
func (h *Hydrator) HydrateHeaderSync() (*walPkg.HeaderSyncEvent, error) {
	return hydrateLastEntry[walPkg.HeaderSyncEvent](h.wal, wal_types.HeaderSync)
}

// HydrateDataSync returns the last DataSync WAL entry.
// Returns nil without error when no DataSync entries exist.
func (h *Hydrator) HydrateDataSync() (*walPkg.DataSyncEvent, error) {
	return hydrateLastEntry[walPkg.DataSyncEvent](h.wal, wal_types.DataSync)
}

// HydrateReconciliation returns the last Reconciliation WAL entry.
// Returns nil without error when no Reconciliation entries exist.
func (h *Hydrator) HydrateReconciliation() (*walPkg.ReconciliationBatchEvent, error) {
	return hydrateLastEntry[walPkg.ReconciliationBatchEvent](h.wal, wal_types.Reconciliation)
}

// ----------------------------------------------------------------------------
// Generic helper
// ----------------------------------------------------------------------------

// hydrateLastEntry scans WAL entries of walType and returns the last one deserialized
// into *T. Returns nil when no matching entry exists.
//
// The two-parameter constraint (T any, P interface{ *T; Deserialize }) is the
// standard Go generics pattern for methods with pointer receivers.
func hydrateLastEntry[T any, P interface {
	*T
	Deserialize([]byte) error
}](w *walPkg.WAL, walType wal_types.WALType) (*T, error) {
	var last *T

	err := w.ReplayEventsByType(walType, func(entry walPkg.WALEntry) error {
		var ev T
		p := P(&ev)
		if err := p.Deserialize(entry.Data); err != nil {
			return fmt.Errorf("deserialize %s at LSN %d: %w", walType, entry.LSN, err)
		}
		last = &ev
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("WAL hydration scan failed for %s: %w", walType, err)
	}

	return last, nil
}
