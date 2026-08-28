package thebesync

// Receiver engine: pull the block tail from one or more peers and apply it
// locally, contiguously, until this node reaches the peers' tip.
//
// P1: single peer, single-worker apply loop.
// P3 (this file): multi-peer round-robin FAILOVER on fetch errors, and a
// PoTS-style TAIL loop that re-checks the head and applies blocks produced during
// sync (bounded rounds so continuous production can't loop forever).
//
// Apply stays strictly sequential (contiguous linkage + the fingerprint chain
// require it); only FETCH is spread across peers. Fetch-ahead concurrency is a
// later optimization and is intentionally not here. Verification, apply, storage,
// and the P2.5 fingerprint gate live in the host's BlockApplier.

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// maxTailRounds bounds the PoTS tail loop: after reaching a peer's advertised
// head, the engine re-checks the head and keeps applying newly produced blocks,
// but at most this many times, so a chain producing blocks faster than we can
// apply still returns (the node finishes catching up via live gossip). A round
// that applies zero new blocks ends the loop early (converged).
const maxTailRounds = 16

// Receiver drives a sync against a set of peers using a host-supplied BlockApplier.
type Receiver struct {
	Applier BlockApplier

	// Progress, when set, is called after each applied batch with (appliedHeight,
	// targetHeight). Optional; keeps this package logging-free.
	Progress func(applied, target uint64)

	// Test seams: when nil these default to the real libp2p FetchHead/FetchBlocks.
	// Overridable in unit tests to exercise failover/tail logic without a network.
	fetchHead   func(ctx context.Context, h host.Host, p peer.ID) (HeadResponse, error)
	fetchBlocks func(ctx context.Context, h host.Host, p peer.ID, from, to uint64) ([][]byte, error)
}

func (r *Receiver) headFn() func(context.Context, host.Host, peer.ID) (HeadResponse, error) {
	if r.fetchHead != nil {
		return r.fetchHead
	}
	return FetchHead
}

func (r *Receiver) blocksFn() func(context.Context, host.Host, peer.ID, uint64, uint64) ([][]byte, error) {
	if r.fetchBlocks != nil {
		return r.fetchBlocks
	}
	return FetchBlocks
}

// SyncFrom brings this node up to the peers' chain tip by log-shipping. It tries
// peers round-robin for each fetch (failover), applies blocks contiguously, and
// then re-checks the head to catch up anything produced during the sync (bounded
// by maxTailRounds). Returns the height reached.
//
// The node must have its genesis block seeded locally before calling; a fresh
// node syncs from localTip+1 and links each block to its predecessor.
func (r *Receiver) SyncFrom(ctx context.Context, h host.Host, peers []peer.ID) (uint64, error) {
	if r.Applier == nil {
		return 0, fmt.Errorf("thebesync: receiver has no BlockApplier")
	}
	if len(peers) == 0 {
		return 0, fmt.Errorf("thebesync: no peers to sync from")
	}

	localHeight, localHash, err := r.Applier.LocalTip()
	if err != nil {
		return 0, fmt.Errorf("thebesync: local tip (node must seed genesis before sync): %w", err)
	}

	prevNumber, prevHash := localHeight, localHash
	certSeen := false // monotonic cert latch (session-local; durable boundary is future work)

	for round := 0; round < maxTailRounds; round++ {
		head, herr := r.headAny(ctx, h, peers)
		if herr != nil {
			return prevNumber, fmt.Errorf("thebesync: head handshake: %w", herr)
		}
		if head.Height <= prevNumber {
			return prevNumber, nil // caught up and stable (no new tail)
		}

		startOfRound := prevNumber
		for prevNumber < head.Height {
			from := prevNumber + 1
			to := from + MaxBlocksPerRequest - 1
			if to > head.Height {
				to = head.Height
			}

			raws, ferr := r.blocksFailover(ctx, h, peers, from, to)
			if ferr != nil {
				return prevNumber, fmt.Errorf("thebesync: fetch [%d..%d]: %w", from, to, ferr)
			}
			if len(raws) == 0 {
				return prevNumber, fmt.Errorf("thebesync: peers returned no blocks for [%d..%d]", from, to)
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

		// PoTS tail: if this round made no progress, we are converged. Otherwise
		// loop to re-check the head for blocks produced during the round.
		if prevNumber == startOfRound {
			break
		}
	}

	return prevNumber, nil
}

// headAny returns the head from the first peer that responds, trying each in
// order (failover). Returns the last error if none respond.
func (r *Receiver) headAny(ctx context.Context, h host.Host, peers []peer.ID) (HeadResponse, error) {
	fn := r.headFn()
	var lastErr error
	for _, p := range peers {
		resp, err := fn(ctx, h, p)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no peers")
	}
	return HeadResponse{}, lastErr
}

// blocksFailover fetches [from..to], trying each peer in turn until one succeeds.
// Returns the last error if every peer fails for this batch.
func (r *Receiver) blocksFailover(ctx context.Context, h host.Host, peers []peer.ID, from, to uint64) ([][]byte, error) {
	fn := r.blocksFn()
	var lastErr error
	for _, p := range peers {
		raws, err := fn(ctx, h, p, from, to)
		if err == nil {
			return raws, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no peers")
	}
	return nil, lastErr
}
