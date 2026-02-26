package priorsync

import (
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	priorsyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// ProtoToTypesMessage converts a protobuf PriorSyncMessage to the domain types.PriorSyncMessage.
func ProtoToTypesMessage(msg *priorsyncpb.PriorSyncMessage) *types.PriorSyncMessage {
	if msg == nil {
		return nil
	}

	result := &types.PriorSyncMessage{}

	if msg.Priorsync != nil {
		ps := &types.PriorSync{
			Blocknumber: msg.Priorsync.Blocknumber,
			Stateroot:   msg.Priorsync.Stateroot,
			Blockhash:   msg.Priorsync.Blockhash,
			Metadata: types.Metadata{
				Checksum: msg.Priorsync.Metadata.GetChecksum(),
				Version:  uint16(msg.Priorsync.Metadata.GetVersion()),
			},
		}
		if msg.Priorsync.Range != nil {
			ps.Range = &types.Range{
				Start: msg.Priorsync.Range.Start,
				End:   msg.Priorsync.Range.End,
			}
		}
		result.Priorsync = ps
	}

	if msg.Ack != nil {
		result.Ack = &types.PriorSyncAck{
			Ok:    msg.Ack.Ok,
			Error: msg.Ack.Error,
		}
	}

	if msg.Phase != nil {
		result.Phase = &types.Phase{
			PresentPhase:    msg.Phase.PresentPhase,
			SuccessivePhase: msg.Phase.SuccessivePhase,
			Success:         msg.Phase.Success,
			Error:           msg.Phase.Error,
		}
	}

	if msg.Headersync != nil {
		result.Headersync = msg.Headersync
	}

	return result
}

// TypesToProtoMessage converts a domain types.PriorSyncMessage to a protobuf PriorSyncMessage.
func TypesToProtoMessage(msg *types.PriorSyncMessage) *priorsyncpb.PriorSyncMessage {
	if msg == nil {
		return nil
	}

	result := &priorsyncpb.PriorSyncMessage{}

	if msg.Priorsync != nil {
		ps := &priorsyncpb.PriorSync{
			Blocknumber: msg.Priorsync.Blocknumber,
			Stateroot:   msg.Priorsync.Stateroot,
			Blockhash:   msg.Priorsync.Blockhash,
			Metadata: &priorsyncpb.Metadata{
				Checksum: msg.Priorsync.Metadata.Checksum,
				Version:  uint32(msg.Priorsync.Metadata.Version),
			},
		}
		if msg.Priorsync.Range != nil {
			ps.Range = &merklepb.Range{
				Start: msg.Priorsync.Range.Start,
				End:   msg.Priorsync.Range.End,
			}
		}
		result.Priorsync = ps
	}

	if msg.Ack != nil {
		result.Ack = &ackpb.Ack{
			Ok:    msg.Ack.Ok,
			Error: msg.Ack.Error,
		}
	}

	if msg.Phase != nil {
		result.Phase = &phasepb.Phase{
			PresentPhase:    msg.Phase.PresentPhase,
			SuccessivePhase: msg.Phase.SuccessivePhase,
			Success:         msg.Phase.Success,
			Error:           msg.Phase.Error,
		}
	}

	if msg.Headersync != nil {
		result.Headersync = msg.Headersync
	}

	return result
}
