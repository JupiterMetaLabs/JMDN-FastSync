package communication

import (
	"context"
	"errors"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
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
	SendPriorSync(ctx context.Context, merkle *merklepb.MerkleSnapshot, peer types.Nodeinfo, data types.PriorSyncMessage) (*types.PriorSyncMessage, error)

	// This is to send the request for merkle tree for the given range.
	SendMerkleRequest(ctx context.Context, peerNode types.Nodeinfo, req *merklepb.MerkleRequestMessage) (*merklepb.MerkleMessage, error)

	// This will be used for the auto sync, nodes will proceed to futher steps without reaching for any confirmations if all the requirements satisfy.
	SendAutoSyncRequest(ctx context.Context, merkle *merklepb.MerkleSnapshot, peer types.Nodeinfo, data types.PriorSyncMessage) (*types.PriorSyncMessage, error)
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
	data types.PriorSyncMessage,
) (*types.PriorSyncMessage, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}
	if merkle_snapshot == nil {
		return nil, errors.New("merkle is nil")
	}

	req := &priorsyncpb.PriorSyncMessage{
		Priorsync: &priorsyncpb.PriorSync{
			Blocknumber:    data.Priorsync.Blocknumber,
			Stateroot:      data.Priorsync.Stateroot,
			Blockhash:      data.Priorsync.Blockhash,
			Merklesnapshot: merkle_snapshot,
			Metadata: &priorsyncpb.Metadata{
				Checksum: data.Priorsync.Metadata.Checksum,
				Version:  uint32(data.Priorsync.Metadata.Version),
			},
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.SYNC_REQUEST,
			SuccessivePhase: constants.SYNC_REQUEST_RESPONSE, // SYNC_REQUEST_RESPONSE because we are returning the response to the client.
			Success:         true,
			Error:           "",
		},
	}

	if data.Priorsync.Range != nil {
		req.Priorsync.Range = &merklepb.Range{
			Start: data.Priorsync.Range.Start,
			End:   data.Priorsync.Range.End,
		}
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using SendProtoDelimited with full peer info for transport selection
	if err := messaging.SendProtoDelimited(
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

	// Convert protobuf response to types.PriorSyncMessage
	result := &types.PriorSyncMessage{
		Ack: &types.PriorSyncAck{
			Ok:    resp.Ack.Ok,
			Error: resp.Ack.Error,
		},
	}

	if resp.Priorsync != nil {
		result.Priorsync = &types.PriorSync{
			Blocknumber: resp.Priorsync.Blocknumber,
			Stateroot:   resp.Priorsync.Stateroot,
			Blockhash:   resp.Priorsync.Blockhash,
			Range: &types.Range{
				Start: resp.Priorsync.Range.Start,
				End:   resp.Priorsync.Range.End,
			},
			Metadata: types.Metadata{
				Checksum: resp.Priorsync.Metadata.Checksum,
				Version:  uint16(resp.Priorsync.Metadata.Version),
			},
		}
		result.Phase = &types.Phase{
			PresentPhase:    resp.Phase.PresentPhase,
			SuccessivePhase: resp.Phase.SuccessivePhase,
			Success:         resp.Phase.Success,
			Error:           resp.Phase.Error,
		}

		result.Ack = &types.PriorSyncAck{
			Ok:    resp.Ack.Ok,
			Error: resp.Ack.Error,
		}
	}

	return result, nil
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

func (c *communication) SendAutoSyncRequest(
	ctx context.Context,
	// As per the observation, data is synced irregularly so we need to check the missing blocks too so merkle check
	merkle_snapshot *merklepb.MerkleSnapshot,
	// this peer is the one we are sending the prior sync to
	peerNode types.Nodeinfo,
	data types.PriorSyncMessage,
) (*types.PriorSyncMessage, error) {
	if c.host == nil {
		return nil, errors.New("host is nil")
	}
	if merkle_snapshot == nil {
		return nil, errors.New("merkle is nil")
	}

	req := &priorsyncpb.PriorSyncMessage{
		Priorsync: &priorsyncpb.PriorSync{
			Blocknumber:    data.Priorsync.Blocknumber,
			Stateroot:      data.Priorsync.Stateroot,
			Blockhash:      data.Priorsync.Blockhash,
			Merklesnapshot: merkle_snapshot,
			Metadata: &priorsyncpb.Metadata{
				Checksum: data.Priorsync.Metadata.Checksum,
				Version:  uint32(data.Priorsync.Metadata.Version),
			},
		},
		Phase: &phasepb.Phase{
			PresentPhase:    constants.SYNC_REQUEST_AUTOPROCEED,
			SuccessivePhase: constants.HEADER_SYNC_REQUEST, // HEADER_SYNC_REQUEST because we are not returning the resp to client. We are autoproceeding to the next phase.
			Success:         true,
			Error:           "",
		},
	}

	if data.Priorsync.Range != nil {
		req.Priorsync.Range = &merklepb.Range{
			Start: data.Priorsync.Range.Start,
			End:   data.Priorsync.Range.End,
		}
	}

	// Prepare peer.AddrInfo from types.Nodeinfo
	peerInfo := libp2p_peer.AddrInfo{
		ID:    peerNode.PeerID,
		Addrs: peerNode.Multiaddr,
	}

	// Prepare response container
	resp := &priorsyncpb.PriorSyncMessage{}

	// Send using SendProtoDelimited with full peer info for transport selection
	if err := messaging.SendProtoDelimited(
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

	// Convert protobuf response to types.PriorSyncMessage
	result := &types.PriorSyncMessage{
		Ack: &types.PriorSyncAck{
			Ok:    resp.Ack.Ok,
			Error: resp.Ack.Error,
		},
	}

	// Convert PriorSync if present
	if resp.Priorsync != nil {
		result.Priorsync = &types.PriorSync{
			Blocknumber: resp.Priorsync.Blocknumber,
			Stateroot:   resp.Priorsync.Stateroot,
			Blockhash:   resp.Priorsync.Blockhash,
			Range: &types.Range{
				Start: resp.Priorsync.Range.Start,
				End:   resp.Priorsync.Range.End,
			},
			Metadata: types.Metadata{
				Checksum: resp.Priorsync.Metadata.Checksum,
				Version:  uint16(resp.Priorsync.Metadata.Version),
			},
		}
		result.Phase = &types.Phase{
			PresentPhase:    resp.Phase.PresentPhase,
			SuccessivePhase: resp.Phase.SuccessivePhase,
			Success:         resp.Phase.Success,
			Error:           resp.Phase.Error,
		}

		result.Ack = &types.PriorSyncAck{
			Ok:    resp.Ack.Ok,
			Error: resp.Ack.Error,
		}
	}

	return result, nil
}
