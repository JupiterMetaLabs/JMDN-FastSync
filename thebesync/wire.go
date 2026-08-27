// Package thebesync is the ThebeSync (FastSync v4) log-shipping sync engine: a
// generic, block-format-agnostic transport for shipping an append-only block log
// between peers over libp2p.
//
// Blocks cross the wire as OPAQUE bytes. This package performs NO block parsing
// and NO consensus verification — all of that lives in the host node behind the
// BlockProvider (serving) and BlockApplier (receiving) interfaces, so this module
// never needs to import the host's types (which would create a module cycle). See
// the host repo's docs/THEBESYNC-DESIGN.md for the trust model.
package thebesync

import "github.com/libp2p/go-libp2p/core/protocol"

const (
	// Protocol IDs (v4 == ThebeSync), consistent with the repo's /fastsync/vN/
	// scheme. Head: peer reports its tip height + tip block hash. GetBlocks:
	// bounded range serve of opaque block bytes.
	HeadProtocol      protocol.ID = "/fastsync/v4/head"
	GetBlocksProtocol protocol.ID = "/fastsync/v4/getblocks"

	// MaxBlocksPerRequest bounds a single GetBlocks range so one request cannot
	// force a server to load an unbounded slice of the chain. A client syncing a
	// long range issues multiple bounded requests. Matches the FastSync DataSync
	// per-request bound (MAX_DATA_PER_REQUEST = 30).
	MaxBlocksPerRequest = 30

	// Frame-size caps for the newline-delimited JSON reads (OOM guard). A GetBlocks
	// response carries full serialized blocks, so it is generous.
	maxHeadReqBytes       int64 = 512
	maxHeadRespBytes      int64 = 4 * 1024
	maxGetBlocksReqBytes  int64 = 512
	maxGetBlocksRespBytes int64 = 64 * 1024 * 1024
)

// HeadRequest is the head-handshake request. Version lets the wire evolve.
type HeadRequest struct {
	Version uint16 `json:"version,omitempty"`
}

// HeadResponse reports the peer's chain tip. Error is set (tip fields zero) when
// the peer cannot serve (e.g. an empty chain) so the client does not sync from it.
type HeadResponse struct {
	Height       uint64 `json:"height"`
	TipBlockHash string `json:"tip_block_hash"`
	Error        string `json:"error,omitempty"`
}

// GetBlocksRequest asks for the inclusive block range [From..To]. The server
// clamps the length to MaxBlocksPerRequest and stops at its own tip.
type GetBlocksRequest struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// GetBlocksResponse returns the opaque serialized blocks the server holds in the
// requested (clamped) range, ascending by height. A slice shorter than requested
// means the server's tip was reached. Error is set only on a server-side failure.
// [][]byte marshals as an array of base64 strings in JSON (no embedded newline).
type GetBlocksResponse struct {
	Blocks [][]byte `json:"blocks"`
	Error  string   `json:"error,omitempty"`
}

// BlockProvider is the server-side source of blocks. The host node implements it
// (e.g. over its block store), returning blocks as opaque bytes.
type BlockProvider interface {
	// LatestHeight returns the local chain tip height. found=false => empty chain.
	LatestHeight() (height uint64, found bool, err error)
	// TipHash returns the hex block hash at the given height.
	TipHash(height uint64) (string, error)
	// RawBlock returns the opaque serialized block at n. found=false => n is beyond
	// the local tip (end of chain), which is not an error.
	RawBlock(n uint64) (raw []byte, found bool, err error)
}

// BlockApplier is the receiver-side verify+apply sink. The host node implements
// it: it parses the opaque bytes, verifies (body binding, committee certificate,
// linkage), applies, stores, and advances its tip — then reports the applied
// block's identity so the engine can chain to the next.
type BlockApplier interface {
	// LocalTip returns the local applied tip (height + hex hash). A freshly
	// genesis-seeded node returns its genesis height/hash.
	LocalTip() (height uint64, hash string, err error)
	// Apply verifies and applies one opaque block that must link to
	// (prevNumber, prevHash). requireCert enforces the monotonic certificate rule.
	// It returns the applied block's (number, hash) and whether it carried a
	// verified certificate.
	Apply(raw []byte, prevNumber uint64, prevHash string, requireCert bool) (number uint64, hash string, hasCert bool, err error)
}
