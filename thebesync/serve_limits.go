package thebesync

// Serve-side limits for GetBlocks. Every request makes the server read up to
// MaxBlocksPerRequest full blocks from its store and encode them, so the
// handler bounds how often one peer can ask, how many serves run at once, and
// how many bytes one response may carry.

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/time/rate"
)

const (
	// Per-peer request rate. A syncing peer issues batches back to back; a
	// request over the rate waits (up to serveWaitMax) rather than failing, so
	// honest catch-up slows down instead of aborting.
	getBlocksPerPeerRate  = rate.Limit(4)
	getBlocksPerPeerBurst = 8
	// getBlocksMaxConcurrent bounds serves in progress across all peers.
	getBlocksMaxConcurrent = 8
	// serveWaitMax is the longest a request waits for a rate token or a serve
	// slot before it is refused with errBusy.
	serveWaitMax = 5 * time.Second
	// maxTrackedPeers bounds the per-peer limiter table.
	maxTrackedPeers = 1024
)

const errBusy = "busy"

// serveByteBudget is the most raw block bytes one GetBlocks response carries.
// Blocks are base64 in JSON (4/3 inflation), so this keeps the encoded response
// under maxGetBlocksRespBytes with headroom. At least one block is always
// returned so a single large block still makes progress.
func serveByteBudget() int64 { return maxGetBlocksRespBytes / 2 }

type serveLimits struct {
	once  sync.Once
	slots chan struct{}

	mu    sync.Mutex
	peers map[peer.ID]*peerLimiter
}

type peerLimiter struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

func (l *serveLimits) init() {
	l.once.Do(func() {
		l.slots = make(chan struct{}, getBlocksMaxConcurrent)
		l.peers = make(map[peer.ID]*peerLimiter)
	})
}

func (l *serveLimits) limiterFor(p peer.ID) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if pl, ok := l.peers[p]; ok {
		pl.lastSeen = now
		return pl.lim
	}
	if len(l.peers) >= maxTrackedPeers {
		// Drop the least recently seen entry. Linear, but only on insert at
		// capacity, and the table is small.
		var oldest peer.ID
		var oldestAt time.Time
		for id, pl := range l.peers {
			if oldestAt.IsZero() || pl.lastSeen.Before(oldestAt) {
				oldest, oldestAt = id, pl.lastSeen
			}
		}
		delete(l.peers, oldest)
	}
	pl := &peerLimiter{lim: rate.NewLimiter(getBlocksPerPeerRate, getBlocksPerPeerBurst), lastSeen: now}
	l.peers[p] = pl
	return pl.lim
}

// acquire waits for p's rate token and a serve slot, up to serveWaitMax.
// On success the caller must call the returned release.
func (l *serveLimits) acquire(p peer.ID) (release func(), ok bool) {
	l.init()
	ctx, cancel := context.WithTimeout(context.Background(), serveWaitMax)
	defer cancel()
	if err := l.limiterFor(p).Wait(ctx); err != nil {
		return nil, false
	}
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, true
	case <-ctx.Done():
		return nil, false
	}
}
