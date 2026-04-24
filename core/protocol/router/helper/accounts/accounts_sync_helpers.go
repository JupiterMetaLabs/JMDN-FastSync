package accounts

import (
	"fmt"
	"sync"

	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum"
	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
)

// AccountsSyncResult is the internal return type for ACCOUNTS_SYNC and HandleAccountsSync.
// Build instances exclusively through resultFactory (accounts_errors.go) — never construct
// this struct inline so the wire shape stays in one place.
//
// Non-final batch (is_last=false, no error):
//   - BatchAck is set; EndOfStream is nil; DiffReady is false.
//   - Stream handler sends BatchAck and loops for the next chunk.
//
// Final batch (is_last=true, no error):
//   - BatchAck is set; DiffReady is true; Missing holds every account the client lacks.
//   - Stream handler pages Missing to the client, then builds and sends EndOfStream.
//
// Error path (any stage — auth failure, checksum mismatch, merge error, etc.):
//   - EndOfStream is set with Ack.Ok=false; BatchAck is nil.
//   - Stream handler sends EndOfStream and closes — stream must not continue.
type AccountsSyncResult struct {
	BatchAck    *accountspb.AccountBatchAck
	EndOfStream *accountspb.AccountSyncEndOfStream
	Missing     []*accountspb.Account
	DiffReady   bool
}

// LockedART wraps a SwappableART and owns the mutex that protects it.
//
// Problem it solves:
//
//	A rogue node can open the AccountsSync stream and fire multiple
//	AccountNonceSyncRequest frames concurrently instead of waiting for each
//	BatchAck before sending the next. SwappableART.Merge holds its own internal
//	mutex for the tree write, but the decode step (art.Decode) that precedes it
//	is unprotected. More critically, if the is_last frame races a mid-stream chunk,
//	Close()+diff can start against a partially merged ART.
//
// Why ownership matters:
//
//	Passing a *sync.Mutex as a parameter to a free function leaves it up to the
//	caller to actually acquire it — any call site that forgets the lock silently
//	introduces a race. By embedding the mutex inside LockedART and exposing only
//	Merge() and Close(), there is no way to call either operation without holding
//	the lock; the type itself enforces the invariant.
//
// Concurrency guarantee:
//
//	Parallel frames from a rogue node are serialised — each decode+merge runs to
//	completion before the next begins. No data is lost or corrupted; only ordering
//	is non-deterministic, which is acceptable because ART merges are commutative.
type LockedART struct {
	mu        sync.Mutex
	swappable *art.SwappableART
}

// NewLockedART wraps an existing SwappableART in a LockedART.
// The caller must not perform any further direct writes to swappable —
// all mutations must go through LockedART.Merge and LockedART.Close.
func NewLockedART(swappable *art.SwappableART) *LockedART {
	return &LockedART{swappable: swappable}
}

// Merge decodes encoded (a zstd-compressed ART chunk) and merges it into the
// underlying SwappableART under the internal mutex.
//
// Decode is intentionally inside the critical section: decoding outside would
// allow two goroutines to race their merges, producing non-deterministic tree
// state. The critical section is bounded by one chunk (≤MAX_ACCOUNT_NONCES keys)
// so the lock hold time is short.
//
// Time:  O(k) where k = keys in the chunk.
// Space: O(k) for the decoded in-memory ART before it is merged and discarded.
func (l *LockedART) Merge(encoded []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	chunk, err := art.Decode(encoded)
	if err != nil {
		return fmt.Errorf("art decode: %w", err)
	}

	if err := l.swappable.Merge(chunk); err != nil {
		return fmt.Errorf("art merge: %w", err)
	}

	return nil
}

// Close flushes remaining hot keys to disk under the internal mutex.
// Must be called once after all chunks have been merged (is_last=true) and
// before ComputeAccountDiff begins its iterator scan.
//
// Time:  O(h) where h = hot keys remaining in memory.
// Space: O(1) beyond the flush buffer managed by SwappableART.
func (l *LockedART) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.swappable.Close()
}

// ARTChecksumValid reports whether the CRC32-IEEE checksum of artBytes matches
// the 4-byte big-endian checksum field from the proto.
// Returns false if csBytes is empty or verification fails.
//
// Time:  O(n) where n = len(artBytes) — one CRC32 pass.
// Space: O(1) — no heap allocations beyond the 4-byte intermediate.
func ARTChecksumValid(artBytes, csBytes []byte) bool {
	if len(csBytes) == 0 {
		return false
	}
	cs := checksum.NewChecksum()
	ok, err := cs.Verify(artBytes, checksum.VersionCRC32, csBytes)
	if err != nil {
		return false
	}
	return ok
}

// ConvertMissingToProto converts the diff result map (nonce → types.Account) to a
// flat proto account slice for wire transport.
//
// Time:  O(n) where n = len(missing).
// Space: O(n) — allocates one *accountspb.Account per entry.
func ConvertMissingToProto(missing map[uint64]*types.Account) []*accountspb.Account {
	out := make([]*accountspb.Account, 0, len(missing))
	for _, acc := range missing {
		if acc == nil {
			continue
		}
		out = append(out, &accountspb.Account{
			DidAddress:  acc.DIDAddress,
			Address:     acc.Address.Bytes(),
			Balance:     "0",
			Nonce:       acc.Nonce,
			AccountType: acc.AccountType,
			CreatedAt:   acc.CreatedAt,
			UpdatedAt:   acc.UpdatedAt,
		})
	}
	return out
}
