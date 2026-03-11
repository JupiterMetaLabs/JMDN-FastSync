package helper

import (
	"fmt"

	nodeinfopb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/nodeinfo"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
)
type nodeinfoHelper struct {}

type Nodeinfo_interface interface{
	ToProto(nodeinfo *types.Nodeinfo) (*nodeinfopb.NodeInfo, error)
	ToNodeinfo(nodeinfo *nodeinfopb.NodeInfo) (*types.Nodeinfo, error)
}

func NewNodeInfoHelper() Nodeinfo_interface {
	return &nodeinfoHelper{}
}

func (h *nodeinfoHelper) ToProto(nf *types.Nodeinfo) (*nodeinfopb.NodeInfo, error) {
	var multiaddrs [][]byte
	for _, m := range nf.Multiaddr {
		multiaddrs = append(multiaddrs, m.Bytes())
	}
	return &nodeinfopb.NodeInfo{
		PeerId:       []byte(nf.PeerID),
		Multiaddrs:   multiaddrs,
		Capabilities: nf.Capabilities,
		PublicKey:    nf.PublicKey,
		Version:      uint32(nf.Version),
		Protocol:     string(nf.Protocol),
	}, nil
}

func (h *nodeinfoHelper) ToNodeinfo(pb *nodeinfopb.NodeInfo) (*types.Nodeinfo, error) {
	if pb == nil {
		return &types.Nodeinfo{}, nil
	}

	peerID, err := peer.IDFromBytes(pb.PeerId)
	if err != nil {
		return &types.Nodeinfo{}, fmt.Errorf("failed to parse peer ID: %w", err)
	}

	var multiaddrs []multiaddr.Multiaddr
	for _, b := range pb.Multiaddrs {
		ma, err := multiaddr.NewMultiaddrBytes(b)
		if err != nil {
			return &types.Nodeinfo{}, fmt.Errorf("failed to parse multiaddr: %w", err)
		}
		multiaddrs = append(multiaddrs, ma)
	}

	return &types.Nodeinfo{
		PeerID:       peerID,
		Multiaddr:    multiaddrs,
		Capabilities: pb.Capabilities,
		PublicKey:    pb.PublicKey,
		Version:      uint16(pb.Version),
		Protocol:     protocol.ID(pb.Protocol),
	}, nil
}

