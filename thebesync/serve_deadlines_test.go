package thebesync

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

func newTCPHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.NoSecurity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

// openStalledRequest sends a GetBlocks request and never reads the response.
func openStalledRequest(t *testing.T, from host.Host, srv peer.AddrInfo) network.Stream {
	t.Helper()
	if err := from.Connect(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	s, err := from.NewStream(context.Background(), srv.ID, GetBlocksProtocol)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONLineErr(s, GetBlocksRequest{From: 1, To: 30}); err != nil {
		t.Fatal(err)
	}
	return s
}

// Peers that send a request and never read the response must not keep serve
// slots: the write deadline resets their streams and an honest peer is served.
// Uses real TCP hosts so the muxer window actually fills and Write blocks.
func TestSlowReadersDoNotStarveServing(t *testing.T) {
	oldBase, oldRate := serveWriteBaseTimeout, serveWriteMinRate
	serveWriteBaseTimeout, serveWriteMinRate = 500*time.Millisecond, 1<<30
	defer func() { serveWriteBaseTimeout, serveWriteMinRate = oldBase, oldRate }()

	srvHost := newTCPHost(t)
	srv := &Server{Provider: bigProvider{tip: 2000, size: 64 * 1024}} // 30 x 64 KiB ≫ stream window
	srvHost.SetStreamHandler(GetBlocksProtocol, srv.GetBlocksHandler)
	info := peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}

	// Distinct peer IDs, so neither the per-peer rate nor the per-peer
	// in-flight cap is what protects the pool here.
	for i := 0; i < getBlocksMaxConcurrent; i++ {
		openStalledRequest(t, newTCPHost(t), info)
	}
	time.Sleep(200 * time.Millisecond) // let every serve block in Write

	honest := newTCPHost(t)
	if err := honest.Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	blocks, err := FetchBlocks(ctx, honest, srvHost.ID(), 1, 1)
	if err != nil {
		t.Fatalf("honest peer starved after %v: %v", time.Since(start), err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if time.Since(start) > serveWaitMax {
		t.Fatalf("honest request took %v, over serveWaitMax", time.Since(start))
	}
}

// One peer ID cannot take more than getBlocksPerPeerInFlight serve slots even
// while its earlier serves are still in progress.
func TestPerPeerInFlightCap(t *testing.T) {
	var l serveLimits
	var rel []func()
	for i := 0; i < getBlocksPerPeerInFlight; i++ {
		r, ok := l.acquire("p")
		if !ok {
			t.Fatalf("acquire %d refused", i)
		}
		rel = append(rel, r)
	}
	done := make(chan bool, 1)
	go func() { _, ok := l.acquire("p"); done <- ok }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("third in-flight serve for one peer was admitted")
		}
	case <-time.After(serveWaitMax + 2*time.Second):
		t.Fatal("acquire did not return")
	}
	// Other peers are unaffected while p is at its cap.
	if r, ok := l.acquire("q"); !ok {
		t.Fatal("other peer refused")
	} else {
		r()
	}
	for _, r := range rel {
		r()
	}
	if r, ok := l.acquire("p"); !ok {
		t.Fatal("peer not admitted after release")
	} else {
		r()
	}
}

// A peer that opens the stream and never sends a request is cut off by the read
// deadline instead of holding the handler goroutine forever.
func TestRequestReadDeadline(t *testing.T) {
	old := serveReadTimeout
	serveReadTimeout = 300 * time.Millisecond
	defer func() { serveReadTimeout = old }()

	srvHost := newTCPHost(t)
	srv := &Server{Provider: bigProvider{tip: 10, size: 16}}
	srvHost.SetStreamHandler(GetBlocksProtocol, srv.GetBlocksHandler)
	info := peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}

	cli := newTCPHost(t)
	if err := cli.Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	s, err := cli.NewStream(context.Background(), srvHost.ID(), GetBlocksProtocol)
	if err != nil {
		t.Fatal(err)
	}
	// Send nothing; the server must give up (bad request or reset) at its read
	// deadline, well before our own 5 s cap.
	_ = s.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	line, rerr := readLine(s, maxGetBlocksRespBytes)
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("server held the stream for %v (read deadline %v)", el, serveReadTimeout)
	}
	if rerr == nil && !strings.Contains(string(line), "bad request") {
		t.Fatalf("unexpected response %q", line)
	}
}

// The client bounds its own wait: a server that accepts the stream and never
// answers must not hang FetchBlocks past clientRequestTimeout.
func TestClientRequestTimeout(t *testing.T) {
	old := clientRequestTimeout
	clientRequestTimeout = 500 * time.Millisecond
	defer func() { clientRequestTimeout = old }()

	srvHost := newTCPHost(t)
	srvHost.SetStreamHandler(GetBlocksProtocol, func(s network.Stream) {
		time.Sleep(5 * time.Second) // never answer in time
		s.Close()
	})
	cli := newTCPHost(t)
	if err := cli.Connect(context.Background(), peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := FetchBlocks(context.Background(), cli, srvHost.ID(), 1, 1)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("FetchBlocks hung for %v", el)
	}
}
