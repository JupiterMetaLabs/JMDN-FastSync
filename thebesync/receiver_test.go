package thebesync

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// mockApplier applies opaque blocks whose bytes are the decimal block number,
// enforcing contiguity, and records the applied sequence.
type mockApplier struct {
	tip     uint64
	hash    string
	applied []uint64
}

func (m *mockApplier) LocalTip() (uint64, string, error) { return m.tip, m.hash, nil }

func (m *mockApplier) Apply(raw []byte, prevNumber uint64, prevHash string, _ bool) (uint64, string, bool, error) {
	n, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, "", false, err
	}
	if n != prevNumber+1 {
		return 0, "", false, fmt.Errorf("non-contiguous: got %d want %d", n, prevNumber+1)
	}
	if prevHash != m.hash {
		return 0, "", false, fmt.Errorf("bad prevHash: got %q want %q", prevHash, m.hash)
	}
	m.tip, m.hash = n, fmt.Sprintf("h%d", n)
	m.applied = append(m.applied, n)
	return m.tip, m.hash, true, nil
}

// fakeNet is an injectable head/blocks source. height is the current tip; onHead,
// when set, updates height per head call (to simulate blocks produced during sync
// for the PoTS tail test). failHead/failBlocks mark peers that error (failover).
type fakeNet struct {
	height     uint64
	onHead     func(call int) uint64
	headCalls  int
	failHead   map[peer.ID]bool
	failBlocks map[peer.ID]bool
}

func (f *fakeNet) head(_ context.Context, _ host.Host, p peer.ID) (HeadResponse, error) {
	if f.failHead[p] {
		return HeadResponse{}, fmt.Errorf("head fail %s", p)
	}
	f.headCalls++
	if f.onHead != nil {
		f.height = f.onHead(f.headCalls)
	}
	return HeadResponse{Height: f.height, TipBlockHash: fmt.Sprintf("h%d", f.height)}, nil
}

func (f *fakeNet) blocks(_ context.Context, _ host.Host, p peer.ID, from, to uint64) ([][]byte, error) {
	if f.failBlocks[p] {
		return nil, fmt.Errorf("blocks fail %s", p)
	}
	out := make([][]byte, 0, to-from+1)
	for n := from; n <= to && n <= f.height; n++ {
		out = append(out, []byte(strconv.FormatUint(n, 10)))
	}
	return out, nil
}

func TestSyncFrom_BasicAndPoTSTail(t *testing.T) {
	app := &mockApplier{} // fresh node: tip 0, hash ""
	// head call 1 -> 5 (initial target); subsequent -> 7 (blocks produced during sync).
	net := &fakeNet{onHead: func(call int) uint64 {
		if call == 1 {
			return 5
		}
		return 7
	}}
	r := &Receiver{Applier: app, fetchHead: net.head, fetchBlocks: net.blocks}

	got, err := r.SyncFrom(context.Background(), nil, []peer.ID{"p0"})
	if err != nil {
		t.Fatalf("SyncFrom error: %v", err)
	}
	if got != 7 {
		t.Fatalf("reached %d, want 7", got)
	}
	want := []uint64{1, 2, 3, 4, 5, 6, 7}
	if len(app.applied) != len(want) {
		t.Fatalf("applied %v, want %v", app.applied, want)
	}
	for i := range want {
		if app.applied[i] != want[i] {
			t.Fatalf("applied[%d]=%d, want %d (seq %v)", i, app.applied[i], want[i], app.applied)
		}
	}
}

func TestSyncFrom_Failover(t *testing.T) {
	app := &mockApplier{}
	// p0 fails both head and blocks; p1 works. Sync must complete via p1.
	net := &fakeNet{
		height:     3,
		failHead:   map[peer.ID]bool{"p0": true},
		failBlocks: map[peer.ID]bool{"p0": true},
	}
	r := &Receiver{Applier: app, fetchHead: net.head, fetchBlocks: net.blocks}

	got, err := r.SyncFrom(context.Background(), nil, []peer.ID{"p0", "p1"})
	if err != nil {
		t.Fatalf("SyncFrom error: %v", err)
	}
	if got != 3 || len(app.applied) != 3 {
		t.Fatalf("reached %d applied %v, want 3 and [1 2 3]", got, app.applied)
	}
}

func TestSyncFrom_AlreadyCaughtUp(t *testing.T) {
	app := &mockApplier{tip: 10, hash: "h10"}
	net := &fakeNet{height: 10}
	r := &Receiver{Applier: app, fetchHead: net.head, fetchBlocks: net.blocks}

	got, err := r.SyncFrom(context.Background(), nil, []peer.ID{"p0"})
	if err != nil {
		t.Fatalf("SyncFrom error: %v", err)
	}
	if got != 10 || len(app.applied) != 0 {
		t.Fatalf("reached %d applied %v, want 10 and none", got, app.applied)
	}
}
