package thebesync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
)

// Regression for testnet 2026-10-27: five AWS validators sat at height 2500
// because the server's slow-reader write deadline (serveWriteBaseTimeout +
// len/serveWriteMinRate) reset every 30-block GetBlocks response over a
// ~1.3 MB/s WireGuard link, and the client retried the identical range forever
// (it only bisected on ErrFrameTooLarge). The server below behaves like that
// link: any response of more than maxFit blocks is aborted with a stream reset,
// exactly what writeResponse does when its deadline expires. The client must
// bisect down to ranges that fit and return the full range.
func TestFetchBlocksBisectsWhenServerResetsSlowReader(t *testing.T) {
	mn := mocknet.New()
	defer mn.Close()
	srvHost, err := mn.GenPeer()
	if err != nil {
		t.Fatal(err)
	}
	cliHost, err := mn.GenPeer()
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if err := mn.ConnectAllButSelf(); err != nil {
		t.Fatal(err)
	}

	const blockSize = 4 * 1024
	const maxFit = 3
	srv := &Server{Provider: bigProvider{tip: 2999, size: blockSize}}
	resets := 0
	srvHost.SetStreamHandler(GetBlocksProtocol, func(s network.Stream) {
		line, err := readLine(s, maxGetBlocksReqBytes)
		if err != nil {
			_ = s.Reset()
			return
		}
		var req GetBlocksRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = s.Reset()
			return
		}
		if clampedTo(req.From, req.To)-req.From+1 > maxFit {
			resets++
			_ = s.Reset() // the slow-reader deadline expired mid-response
			return
		}
		b, _ := json.Marshal(srv.buildGetBlocks(req))
		_ = writeResponse(s, b)
		_ = s.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	from, to := uint64(2501), uint64(2530)
	blocks, err := FetchBlocks(ctx, cliHost, srvHost.ID(), from, to)
	if err != nil {
		t.Fatalf("FetchBlocks with a resetting server: %v (resets=%d)", err, resets)
	}
	if got, want := len(blocks), int(to-from+1); got != want {
		t.Fatalf("got %d blocks, want %d (resets=%d)", got, want, resets)
	}
	for i, b := range blocks {
		n := from + uint64(i)
		if len(b) != blockSize || b[0] != byte(n%251) {
			t.Fatalf("block %d: wrong content (len %d, first byte %d)", n, len(b), b[0])
		}
	}
	if resets == 0 {
		t.Fatal("test did not exercise the reset path")
	}
}

// A cancelled caller is not a size problem: no bisecting, the error comes back.
func TestShouldBisectIgnoresCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldBisect(ctx, ErrFrameTooLarge) {
		t.Fatal("cancelled context must not bisect")
	}
	if !shouldBisect(context.Background(), ErrFrameTooLarge) {
		t.Fatal("oversize frame must bisect")
	}
	// A timeout is a stalled or unreachable peer, not a size problem.
	for _, err := range []error{os.ErrDeadlineExceeded, context.DeadlineExceeded, errors.New("failed to dial: context deadline exceeded")} {
		if shouldBisect(context.Background(), err) {
			t.Fatalf("timeout %q must not bisect", err)
		}
	}
}
