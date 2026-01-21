package types

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

type Nodeinfo struct {
	PeerID       peer.ID
	Multiaddr    []multiaddr.Multiaddr
	Capabilities []string
	PublicKey    []byte
	Version      uint64
}
