package communication

import (
	"context"
	"errors"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/libp2p/go-libp2p/core/host"
	libp2p_peer "github.com/libp2p/go-libp2p/core/peer"
)

type communication struct {
	host            host.Host
	protocolVersion uint16
}

type Communicator interface {
	// SendPriorSync sends a PriorSync request to a specific peer and returns the response
	SendPriorSync(ctx context.Context, merkle *merklepb.MerkleSnapshot, peer types.Nodeinfo, data priorsyncpb.PriorSyncMessage) (*priorsyncpb.PriorSyncMessage, error)

	// This is to send the request for merkle tree for the given range.
	SendMerkleRequest(ctx context.Context, peerNode types.Nodeinfo, req *merklepb.MerkleRequestMessage) (*merklepb.MerkleMessage, error)

	// SendHeaderSyncRequest sends a HeaderSyncRequest to a peer and returns the HeaderSyncResponse.
	SendHeaderSyncRequest(ctx context.Context, peerNode types.Nodeinfo, req *headersyncpb.HeaderSyncRequest) (*headersyncpb.HeaderSyncResponse, error)

	// SendDataSyncRequest sends a DataSyncRequest to a peer and returns the DataSyncResponse.
	SendDataSyncRequest(ctx context.Context, peerNode types.Nodeinfo, req *datasyncpb.DataSyncRequest) (*datasyncpb.DataSyncResponse, error)

	SendAvailabilityRequest(ctx context.Context, peerNode types.Nodeinfo, req *availabilitypb.AvailabilityRequest) (*availabilitypb.AvailabilityResponse, error)

	SendPoTSRequest(ctx context.Context, peerNode types.Nodeinfo, req *potspb.PoTSRequest) (*potspb.PoTSResponse, error)
}

func NewCommunication(host host.Host, protocolVersion uint16) Communicator {
	return &communication{
		host:            host,
		protocolVersion: protocolVersion,
	}
}

// SendPriorSync sends a PriorSync message to a peer and returns the response.
// Usually you'll do node.NewStream(ctx, peerID, protoID) and write the payload.
func (c *communication) SendPriorSync(
	ctx context.Context,
	// As per the observation, data is synced irregularly so we need to check the missing blocks too so merkle check
	merkle_snapshot *merklepb.MerkleSnapshot,
	// this peer is the one we are sending the prior sync to
	peerNode types.Nodeinfo,
	data priorsyncpb.PriorSyncMessage,
) (*priorsyncpb.PriorSyncMessage, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}
	if merkle_snapshot == nil {
		return nil, errors.New("merkle is nil")
	}

	// Make sure we have a valid Phase
	phase := data.Phase
	if phase == nil {
		phase = &phasepb.Phase{}
	}
	// Always set the proper phase strings for this request
	phase.PresentPhase = constants.SYNC_REQUEST
	phase.SuccessivePhase = constants.SYNC_REQUEST_RESPONSE
	phase.Success = true

	req := &priorsyncpb.PriorSyncMessage{
		Priorsync: &priorsyncpb.PriorSync{
			Blocknumber:    data.Priorsync.GetBlocknumber(),
			Stateroot:      data.Priorsync.GetStateroot(),
			Blockhash:      data.Priorsync.GetBlockhash(),
			Merklesnapshot: merkle_snapshot,
			Metadata: &priorsyncpb.Metadata{
				Checksum: data.Priorsync.GetMetadata().GetChecksum(),
				Version:  data.Priorsync.GetMetadata().GetVersion(),
			},
		},
		Phase: phase,
	}

	if data.Priorsync.GetRange() != nil {
		req.Priorsync.Range = &merklepb.Range{
			Start: data.Priorsync.GetRange().GetStart(),
			End:   data.Priorsync.GetRange().GetEnd(),
		}
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using SendProtoDelimitedWithHeartbeat for heartbeat-aware read loop
	if err := messaging.SendProtoDelimitedWithHeartbeat(
		ctx,
		c.protocolVersion,
		c.host,
		peerInfo, // Pass full AddrInfo for transport selection
		constants.PriorSyncProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send priorsync: " + err.Error())
	}

	// Check acknowledgment
	if resp.Ack == nil {
		return nil, errors.New("no acknowledgment received")
	}
	if !resp.Ack.Ok {
		return nil, errors.New("sync failed: " + resp.Ack.Error)
	}

	return resp, nil
}

// SendMerkleRequest sends a MerkleRequestMessage to a peer and returns the response.
func (c *communication) SendMerkleRequest(
	ctx context.Context,
	peerNode types.Nodeinfo,
	req *merklepb.MerkleRequestMessage,
) (*merklepb.MerkleMessage, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &merklepb.MerkleMessage{}

	// Send using SendProtoDelimited with MerkleProtocol
	if err := messaging.SendProtoDelimited(
		ctx,
		c.protocolVersion,
		c.host,
		peerInfo,
		constants.MerkleProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send merkle request: " + err.Error())
	}

	return resp, nil
}

// SendHeaderSyncRequest sends a HeaderSyncRequest to a peer and returns the HeaderSyncResponse.
func (c *communication) SendHeaderSyncRequest(
	ctx context.Context,
	peerNode types.Nodeinfo,
	req *headersyncpb.HeaderSyncRequest,
) (*headersyncpb.HeaderSyncResponse, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &headersyncpb.HeaderSyncResponse{}

	// Send using SendProtoDelimited with HeaderSyncProtocol
	if err := messaging.SendProtoDelimited(
		ctx,
		c.protocolVersion,
		c.host,
		peerInfo,
		constants.HeaderSyncProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send header sync request: " + err.Error())
	}

	return resp, nil
}

// SendDataSyncRequest sends a DataSyncRequest to a peer and returns the DataSyncResponse.
func (c *communication) SendDataSyncRequest(
	ctx context.Context,
	peerNode types.Nodeinfo,
	req *datasyncpb.DataSyncRequest,
) (*datasyncpb.DataSyncResponse, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &datasyncpb.DataSyncResponse{}

	// Send using SendDataSyncProtoDelimitedWithHeartbeat to keep stream alive during long SQL queries
	if err := messaging.SendDataSyncProtoDelimitedWithHeartbeat(
		ctx,
		c.protocolVersion,
		c.host,
		peerInfo,
		constants.DataSyncProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send data sync request: " + err.Error())
	}

	return resp, nil
}

// SendAvailabilityRequest sends an AvailabilityRequest to a peer and returns the AvailabilityResponse.
func (c *communication) SendAvailabilityRequest(
	ctx context.Context,
	peerNode types.Nodeinfo,
	req *availabilitypb.AvailabilityRequest,
) (*availabilitypb.AvailabilityResponse, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &availabilitypb.AvailabilityResponse{}

	// Send using SendProtoDelimited
	if err := messaging.SendProtoDelimited(
		ctx,
		c.protocolVersion,
		c.host,
		peerInfo,
		constants.AvailabilityProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send availability request: " + err.Error())
	}

	return resp, nil
}

func (c *communication) SendPoTSRequest(
	ctx context.Context,
	peerNode types.Nodeinfo,
	req *potspb.PoTSRequest,
) (*potspb.PoTSResponse, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &potspb.PoTSResponse{}

	// Send using SendProtoDelimited
	if err := messaging.SendPoTSProtoDelimitedWithHeartbeat(
		ctx,
		c.protocolVersion,
		c.host,
		peerInfo,
		constants.PoTSProtocol,
		req,
		resp,
	); err != nil {
		return nil, errors.New("failed to send pots request: " + err.Error())
	}

	return resp, nil
}