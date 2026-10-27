package thebesync

// Client side: open a stream to a peer and issue the head handshake or a bounded
// GetBlocks request. Returns raw (opaque) block bytes for the host to parse.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// clientRequestTimeout bounds one round trip (write request, read response)
// when ctx carries no earlier deadline, so a peer that accepts the stream and
// then stalls cannot hang a sync forever. Sized for a full byte-budget response
// over a slow link; a var so tests can shorten it.
var clientRequestTimeout = 3 * time.Minute

// setRequestDeadline applies the earlier of ctx's deadline and
// clientRequestTimeout to the stream.
func setRequestDeadline(ctx context.Context, stream network.Stream) {
	d := time.Now().Add(clientRequestTimeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(d) {
		d = cd
	}
	_ = stream.SetDeadline(d)
}

// FetchHead opens the head-handshake stream to peer p and returns its chain tip.
// A peer that reports an Error (e.g. empty chain) surfaces as a non-nil error.
func FetchHead(ctx context.Context, h host.Host, p peer.ID) (HeadResponse, error) {
	var resp HeadResponse

	stream, err := h.NewStream(ctx, p, HeadProtocol)
	if err != nil {
		return resp, fmt.Errorf("thebesync FetchHead: open stream to %s: %w", p, err)
	}
	defer stream.Close()
	setRequestDeadline(ctx, stream)

	if err := writeJSONLineErr(stream, HeadRequest{Version: 1}); err != nil {
		return resp, fmt.Errorf("thebesync FetchHead: write: %w", err)
	}
	line, err := readLine(stream, maxHeadRespBytes)
	if err != nil {
		return resp, fmt.Errorf("thebesync FetchHead: read: %w", err)
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return resp, fmt.Errorf("thebesync FetchHead: decode: %w", err)
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("thebesync FetchHead: peer %s error: %s", p, resp.Error)
	}
	return resp, nil
}

// FetchBlocks requests the inclusive range [from..to] from peer p and returns the
// opaque block bytes. The server clamps to MaxBlocksPerRequest and stops at its
// tip, so the returned slice may be shorter than requested.
//
// A server may also stop early to keep a response under its byte budget, so a
// short response is followed up from the next height; only an empty response
// means the server's tip was reached.
func FetchBlocks(ctx context.Context, h host.Host, p peer.ID, from, to uint64) ([][]byte, error) {
	if to < from {
		return nil, fmt.Errorf("thebesync FetchBlocks: invalid range [%d..%d]", from, to)
	}
	var out [][]byte
	for cur := from; cur <= to; {
		blocks, err := fetchBlocksSplit(ctx, h, p, cur, to)
		if err != nil {
			if len(out) > 0 {
				return out, nil // keep the progress made; the caller resumes from here
			}
			return nil, err
		}
		if len(blocks) == 0 {
			break // tip reached
		}
		out = append(out, blocks...)
		cur += uint64(len(blocks))
	}
	return out, nil
}

// fetchBlocksSplit fetches [from..to], bisecting when a response exceeds the
// read cap or the server aborts it because this link drains too slowly.
func fetchBlocksSplit(ctx context.Context, h host.Host, p peer.ID, from, to uint64) ([][]byte, error) {
	blocks, err := fetchBlocksOnce(ctx, h, p, from, to)
	if err == nil || to == from || !shouldBisect(ctx, err) {
		return blocks, err
	}
	// Either the whole range did not fit under maxGetBlocksRespBytes (blocks
	// carry STARK proofs, so 30 of them can exceed the cap), or the server reset
	// the stream because its size-derived write deadline expired: it allows
	// serveWriteBaseTimeout + len/serveWriteMinRate, so a link slower than
	// serveWriteMinRate can never receive a full 30-block response and, without
	// this, retried the identical range forever (testnet 2026-10-27: AWS over
	// WireGuard at ~1.3 MB/s, five nodes stuck at 2500). Bisect: fetch the halves
	// and concatenate. Each half is strictly smaller, so this terminates; a single
	// block that still fails is reported as the error it is.
	mid := from + (to-from)/2
	lo, err := fetchBlocksSplit(ctx, h, p, from, mid)
	if err != nil {
		return nil, err
	}
	if uint64(len(lo)) < mid-from+1 {
		// server stopped early inside the lower half (tip or byte budget);
		// FetchBlocks resumes from the next height
		return lo, nil
	}
	hi, err := fetchBlocksSplit(ctx, h, p, mid+1, to)
	if err != nil {
		return nil, err
	}
	return append(lo, hi...), nil
}

// shouldBisect reports whether a failed GetBlocks round trip should be retried
// as two smaller ranges: the response exceeded the read cap, or the stream was
// reset / timed out mid-response (the server's slow-reader deadline, or our own
// request deadline). Never when the caller's context is done — that is a
// cancellation, not a size problem.
func shouldBisect(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, ErrFrameTooLarge) || errors.Is(err, network.ErrReset) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	// Transports do not all wrap their reset as network.ErrReset (QUIC surfaces
	// "stream reset (remote): ... canceled by remote"); match the text as well.
	msg := err.Error()
	return strings.Contains(msg, "stream reset") || strings.Contains(msg, "deadline exceeded")
}

// fetchBlocksOnce is one GetBlocks round trip for [from..to] with no splitting.
func fetchBlocksOnce(ctx context.Context, h host.Host, p peer.ID, from, to uint64) ([][]byte, error) {
	stream, err := h.NewStream(ctx, p, GetBlocksProtocol)
	if err != nil {
		return nil, fmt.Errorf("thebesync FetchBlocks: open stream to %s: %w", p, err)
	}
	defer stream.Close()
	setRequestDeadline(ctx, stream)

	if err := writeJSONLineErr(stream, GetBlocksRequest{From: from, To: to}); err != nil {
		return nil, fmt.Errorf("thebesync FetchBlocks: write: %w", err)
	}
	line, err := readLine(stream, maxGetBlocksRespBytes)
	if err != nil {
		return nil, fmt.Errorf("thebesync FetchBlocks: read: %w", err)
	}
	var resp GetBlocksResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("thebesync FetchBlocks: decode: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("thebesync FetchBlocks: peer %s error: %s", p, resp.Error)
	}
	return resp.Blocks, nil
}
