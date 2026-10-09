package thebesync

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
)

// bigProvider serves `tip+1` blocks of `size` bytes each (block n = byte n%251 repeated).
type bigProvider struct {
	tip  uint64
	size int
}

func (b bigProvider) LatestHeight() (uint64, bool, error) { return b.tip, true, nil }
func (b bigProvider) TipHash(h uint64) (string, error)    { return fmt.Sprintf("0x%064x", h), nil }
func (b bigProvider) RawBlock(n uint64) ([]byte, bool, error) {
	if n > b.tip {
		return nil, false, nil
	}
	return bytes.Repeat([]byte{byte(n % 251)}, b.size), true, nil
}

// Regression for testnet 2026-10-09: a 30-block GetBlocks response of proof-carrying
// blocks exceeded the client read cap; readLine returned the truncated frame as
// success and the client failed with "unexpected end of JSON input", so any node
// > ~22 blocks behind could never catch up. FetchBlocks must bisect instead.
func TestFetchBlocksBisectsWhenResponseExceedsCap(t *testing.T) {
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

	const blockSize = 64 * 1024 // base64 in JSON ≈ 87 KB per block
	prov := bigProvider{tip: 999, size: blockSize}
	srv := &Server{Provider: prov}
	srvHost.SetStreamHandler(GetBlocksProtocol, srv.GetBlocksHandler)

	// Cap admits ~5 blocks: a 30-block batch (≈2.6 MB) must trigger bisection.
	old := maxGetBlocksRespBytes
	maxGetBlocksRespBytes = 5 * 90 * 1024
	defer func() { maxGetBlocksRespBytes = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	from, to := uint64(940), uint64(969)
	blocks, err := FetchBlocks(ctx, cliHost, srvHost.ID(), from, to)
	if err != nil {
		t.Fatalf("FetchBlocks: %v", err)
	}
	if got, want := len(blocks), int(to-from+1); got != want {
		t.Fatalf("got %d blocks, want %d", got, want)
	}
	for i, b := range blocks {
		n := from + uint64(i)
		if len(b) != blockSize || b[0] != byte(n%251) {
			t.Fatalf("block %d: wrong content (len %d, first byte %d)", n, len(b), b[0])
		}
	}

	// Range that runs past the tip inside the lower half: must return what exists, no error.
	prov2 := bigProvider{tip: 944, size: blockSize}
	srvHost.SetStreamHandler(GetBlocksProtocol, (&Server{Provider: prov2}).GetBlocksHandler)
	blocks, err = FetchBlocks(ctx, cliHost, srvHost.ID(), 940, 969)
	if err != nil {
		t.Fatalf("FetchBlocks past tip: %v", err)
	}
	if len(blocks) != 5 {
		t.Fatalf("past tip: got %d blocks, want 5", len(blocks))
	}

	// A single block larger than the cap is a hard error, not a silent truncation.
	maxGetBlocksRespBytes = 1024
	if _, err := FetchBlocks(ctx, cliHost, srvHost.ID(), 940, 940); err == nil {
		t.Fatal("single oversize block: want error, got nil")
	}
}

func TestReadLineReportsTruncation(t *testing.T) {
	// A frame cut by the cap must surface ErrFrameTooLarge rather than a partial line.
	mn := mocknet.New()
	defer mn.Close()
	a, _ := mn.GenPeer()
	b, _ := mn.GenPeer()
	_ = mn.LinkAll()
	_ = mn.ConnectAllButSelf()
	b.SetStreamHandler("/t/big", func(s network.Stream) {
		defer s.Close()
		_, _ = s.Write(bytes.Repeat([]byte("x"), 4096)) // no newline within 1 KiB
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := a.NewStream(ctx, b.ID(), "/t/big")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readLine(s, 1024); err != ErrFrameTooLarge {
		t.Fatalf("want ErrFrameTooLarge, got %v", err)
	}
}
