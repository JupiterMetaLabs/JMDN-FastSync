package types

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
)

/*
- If version is not set, it will be default set to 1
V1: Supports only TCP connections.
V2: supports both TCP and QUIC connections. Prioritize QUIC connections, fallback to TCP if QUIC fails.
*/
type Nodeinfo struct {
	PeerID       peer.ID
	Multiaddr    []multiaddr.Multiaddr
	Capabilities []string
	PublicKey    []byte
	Version      uint16
	Protocol     protocol.ID
	BlockInfo    BlockInfo
}

type BlockInfo interface {
	GetBlockNumber() uint64
	GetBlockDetails() PriorSync
	NewBlockIterator(start, end uint64, batchsize int) BlockIterator
}

type BlockIterator interface {
	Next() ([]*ZKBlock, error)
	Prev() ([]*ZKBlock, error)
	Close()
}
