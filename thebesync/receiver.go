package thebesync

// Receiver engine (P1): pull the block tail from a peer and apply it locally,
// contiguously, until this node reaches the peer's tip. Single-worker;
// concurrency, failover, and PoTS tail catch-up land in P3. Verification, apply,
// storage, and the P2.5 fingerprint gate (P2) live in the host's BlockApplier —
// this engine only orchestrates fetch → apply → advance.

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Receiver drives a sync against a peer using a host-supplied BlockApplier.
type Receiver struct {
	Applier BlockApplier
	// Progress, when set, is called after each applied batch with (appliedHeight,
	// targetHeight). Optional; keeps this package logging-free.
	Progress func(applied, target uint64)
}

// SyncFrom brings this node up to peer p's chain tip by log-shipping: head
// handshake, then bounded GetBlocks batches applied in ascending, contiguous
// order. Returns the height reached.
//
// The node must have its genesis block seeded locally before calling; a fresh
// node syncs from localTip+1 and links each block to its predecessor, with the
// locally-seeded genesis hash as the anchor (BlockApplier.LocalTip).
func (r *Receiver) SyncFrom(ctx context.Context, h host.Host, p peer.ID) (uint64, error) {
	if r.Applier == nil {
		return 0, fmt.Errorf("thebesync: receiver has no BlockApplier")
	}

	head, err := FetchHead(ctx, h, p)
	if err != nil {
		return 0, fmt.Errorf("thebesync: head handshake: %w", err)
	}

	localHeight, localHash, err := r.Applier.LocalTip()
	if err != nil {
		return 0, fmt.Errorf("thebesync: local tip (node must seed genesis before sync): %w", err)
	}
	if head.Height <= localHeight {
		return localHeight, nil // already at or beyond the peer's tip
	}

	prevNumber, prevHash := localHeight, localHash

	// Monotonic cert latch: once a certified block is applied, every later block
	// must carry a certificate (a peer cannot downgrade later blocks to legacy).
	// Session-local in P1; a durable first-certified-height boundary is P2/P3.
	certSeen := false

	for prevNumber < head.Height {
		from := prevNumber + 1
		to := from + MaxBlocksPerRequest - 1
		if to > head.Height {
			to = head.Height
		}

		raws, ferr := FetchBlocks(ctx, h, p, from, to)
		if ferr != nil {
			return prevNumber, fmt.Errorf("thebesync: fetch [%d..%d]: %w", from, to, ferr)
		}
		if len(raws) == 0 {
			return prevNumber, fmt.Errorf("thebesync: peer returned no blocks for [%d..%d] (tip regressed?)", from, to)
		}

		for _, raw := range raws {
			number, hash, hasCert, aerr := r.Applier.Apply(raw, prevNumber, prevHash, certSeen)
			if aerr != nil {
				return prevNumber, fmt.Errorf("thebesync: apply after %d: %w", prevNumber, aerr)
			}
			if hasCert {
				certSeen = true
			}
			prevNumber, prevHash = number, hash
		}

		if r.Progress != nil {
			r.Progress(prevNumber, head.Height)
		}
		if cerr := ctx.Err(); cerr != nil {
			return prevNumber, fmt.Errorf("thebesync: cancelled at %d: %w", prevNumber, cerr)
		}
	}

	return prevNumber, nil
}
