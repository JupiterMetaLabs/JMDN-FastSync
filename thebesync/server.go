package thebesync

// Server side: read-only libp2p stream handlers for the head handshake and the
// bounded GetBlocks range serve, backed by a host-supplied BlockProvider. These
// never mutate state — they answer explicit peer requests.

import (
	"encoding/json"

	"github.com/libp2p/go-libp2p/core/network"
)

// Server holds the BlockProvider and exposes libp2p stream handlers. Register its
// methods on the host:
//
//	srv := &thebesync.Server{Provider: myProvider}
//	h.SetStreamHandler(thebesync.HeadProtocol, srv.HeadHandler)
//	h.SetStreamHandler(thebesync.GetBlocksProtocol, srv.GetBlocksHandler)
type Server struct {
	Provider BlockProvider
}

// HeadHandler answers a HeadRequest with this node's chain tip.
func (s *Server) HeadHandler(stream network.Stream) {
	defer stream.Close()
	drainLine(stream, maxHeadReqBytes) // v1 request carries nothing we need
	writeJSONLine(stream, s.buildHead())
}

func (s *Server) buildHead() HeadResponse {
	height, found, err := s.Provider.LatestHeight()
	if err != nil {
		return HeadResponse{Error: "tip unavailable"}
	}
	if !found {
		return HeadResponse{Error: "no blocks"}
	}
	resp := HeadResponse{Height: height}
	if hash, herr := s.Provider.TipHash(height); herr == nil {
		resp.TipBlockHash = hash
	}
	return resp
}

// GetBlocksHandler answers a GetBlocksRequest with the opaque blocks in the
// clamped range. Read-only.
func (s *Server) GetBlocksHandler(stream network.Stream) {
	defer stream.Close()

	reqBytes, err := readLine(stream, maxGetBlocksReqBytes)
	if err != nil || len(reqBytes) == 0 {
		writeJSONLine(stream, GetBlocksResponse{Error: "bad request"})
		return
	}
	var req GetBlocksRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		writeJSONLine(stream, GetBlocksResponse{Error: "bad request"})
		return
	}
	writeJSONLine(stream, s.buildGetBlocks(req))
}

// buildGetBlocks loads the contiguous opaque blocks in [From..clamped-To]. It
// stops at the first not-found (tip reached) or read error and returns the
// contiguous prefix so the client can retry the remainder.
func (s *Server) buildGetBlocks(req GetBlocksRequest) GetBlocksResponse {
	if req.To < req.From {
		return GetBlocksResponse{Error: "invalid range"}
	}
	to := clampedTo(req.From, req.To)

	blocks := make([][]byte, 0, to-req.From+1)
	for n := req.From; n <= to; n++ {
		raw, found, err := s.Provider.RawBlock(n)
		if err != nil || !found {
			break // tip reached or read error — return the contiguous prefix
		}
		blocks = append(blocks, raw)
	}
	return GetBlocksResponse{Blocks: blocks}
}

// clampedTo returns the inclusive upper bound after applying the
// MaxBlocksPerRequest length cap. Written as (to-from >= Max) rather than
// (to-from+1 > Max) so a malicious to (e.g. MaxUint64) cannot overflow the +1 and
// bypass the cap. Caller must guarantee to >= from.
func clampedTo(from, to uint64) uint64 {
	if to-from >= MaxBlocksPerRequest {
		return from + MaxBlocksPerRequest - 1
	}
	return to
}
