package thebesync

import (
	"bytes"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type sizedProvider struct {
	tip  uint64
	size int
}

func (p sizedProvider) LatestHeight() (uint64, bool, error) { return p.tip, true, nil }
func (p sizedProvider) TipHash(uint64) (string, error)      { return "0x0", nil }
func (p sizedProvider) RawBlock(n uint64) ([]byte, bool, error) {
	if n > p.tip {
		return nil, false, nil
	}
	return bytes.Repeat([]byte{1}, p.size), true, nil
}

// The server stops filling a response at the byte budget, but always sends
// at least one block.
func TestBuildGetBlocks_ByteBudget(t *testing.T) {
	old := maxGetBlocksRespBytes
	maxGetBlocksRespBytes = 10 * 1000 // budget = 5000 raw bytes
	defer func() { maxGetBlocksRespBytes = old }()

	s := &Server{Provider: sizedProvider{tip: 100, size: 1000}}
	resp := s.buildGetBlocks(GetBlocksRequest{From: 1, To: 30})
	if len(resp.Blocks) != 5 {
		t.Fatalf("got %d blocks, want 5 under a 5000-byte budget", len(resp.Blocks))
	}

	big := &Server{Provider: sizedProvider{tip: 100, size: 50_000}}
	if got := len(big.buildGetBlocks(GetBlocksRequest{From: 1, To: 30}).Blocks); got != 1 {
		t.Fatalf("oversized block: got %d, want exactly 1", got)
	}
}

func TestServeLimits_ConcurrencyCap(t *testing.T) {
	var l serveLimits
	var releases []func()
	for i := 0; i < getBlocksMaxConcurrent; i++ {
		r, ok := l.acquire(peer.ID(string(rune('a' + i))))
		if !ok {
			t.Fatalf("slot %d refused", i)
		}
		releases = append(releases, r)
	}
	start := time.Now()
	done := make(chan bool, 1)
	go func() { _, ok := l.acquire("z"); done <- ok }()
	time.Sleep(50 * time.Millisecond)
	releases[0]() // freeing a slot lets the waiter in
	if ok := <-done; !ok {
		t.Fatal("waiter should get the freed slot")
	}
	if time.Since(start) > serveWaitMax {
		t.Fatal("waiter waited too long")
	}
}

func TestServeLimits_PeerTableBounded(t *testing.T) {
	var l serveLimits
	l.init()
	for i := 0; i < maxTrackedPeers+50; i++ {
		l.limiterFor(peer.ID(string(rune(0x1000 + i))))
	}
	if len(l.peers) > maxTrackedPeers {
		t.Fatalf("peer table = %d, want <= %d", len(l.peers), maxTrackedPeers)
	}
}
