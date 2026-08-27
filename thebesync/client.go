package thebesync

// Client side: open a stream to a peer and issue the head handshake or a bounded
// GetBlocks request. Returns raw (opaque) block bytes for the host to parse.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// FetchHead opens the head-handshake stream to peer p and returns its chain tip.
// A peer that reports an Error (e.g. empty chain) surfaces as a non-nil error.
func FetchHead(ctx context.Context, h host.Host, p peer.ID) (HeadResponse, error) {
	var resp HeadResponse

	stream, err := h.NewStream(ctx, p, HeadProtocol)
	if err != nil {
		return resp, fmt.Errorf("thebesync FetchHead: open stream to %s: %w", p, err)
	}
	defer stream.Close()

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
func FetchBlocks(ctx context.Context, h host.Host, p peer.ID, from, to uint64) ([][]byte, error) {
	if to < from {
		return nil, fmt.Errorf("thebesync FetchBlocks: invalid range [%d..%d]", from, to)
	}

	stream, err := h.NewStream(ctx, p, GetBlocksProtocol)
	if err != nil {
		return nil, fmt.Errorf("thebesync FetchBlocks: open stream to %s: %w", p, err)
	}
	defer stream.Close()

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
