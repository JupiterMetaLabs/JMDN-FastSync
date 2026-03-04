package WAL

import (
	"encoding/json"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"google.golang.org/protobuf/proto"
)

// HeaderSyncEvent represents a header synchronization event
type HeaderSyncEvent struct {
	wal_types.BaseEvent
	Response *headersync.HeaderSyncResponse `json:"-"` // Proto message, not JSON serialized
	// We store the proto as bytes in ProtoData for JSON serialization
	ProtoData []byte `json:"proto_data"`
}

func (e *HeaderSyncEvent) GetType() wal_types.WALType {
	return wal_types.HeaderSync
}

func (e *HeaderSyncEvent) GetOperation() wal_types.EventOperation {
	return e.Operation
}

func (e *HeaderSyncEvent) Serialize() ([]byte, error) {
	e.Type = wal_types.HeaderSync

	// Marshal proto to bytes
	if e.Response != nil {
		protoBytes, err := proto.Marshal(e.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal proto: %w", err)
		}
		e.ProtoData = protoBytes
	}

	return json.Marshal(e)
}

func (e *HeaderSyncEvent) Deserialize(data []byte) error {
	if err := json.Unmarshal(data, e); err != nil {
		return err
	}

	// Unmarshal proto from bytes
	if len(e.ProtoData) > 0 {
		e.Response = &headersync.HeaderSyncResponse{}
		if err := proto.Unmarshal(e.ProtoData, e.Response); err != nil {
			return fmt.Errorf("failed to unmarshal proto: %w", err)
		}
	}

	return nil
}

// MerkleSyncEvent represents a Merkle tree synchronization event
type MerkleSyncEvent struct {
	wal_types.BaseEvent
	Message   *merkle.MerkleMessage `json:"-"` // Proto message
	ProtoData []byte                `json:"proto_data"`
}

func (e *MerkleSyncEvent) GetType() wal_types.WALType {
	return wal_types.MerkleSync
}

func (e *MerkleSyncEvent) GetOperation() wal_types.EventOperation {
	return e.Operation
}

func (e *MerkleSyncEvent) Serialize() ([]byte, error) {
	e.Type = wal_types.MerkleSync

	if e.Message != nil {
		protoBytes, err := proto.Marshal(e.Message)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal proto: %w", err)
		}
		e.ProtoData = protoBytes
	}

	return json.Marshal(e)
}

func (e *MerkleSyncEvent) Deserialize(data []byte) error {
	if err := json.Unmarshal(data, e); err != nil {
		return err
	}

	if len(e.ProtoData) > 0 {
		e.Message = &merkle.MerkleMessage{}
		if err := proto.Unmarshal(e.ProtoData, e.Message); err != nil {
			return fmt.Errorf("failed to unmarshal proto: %w", err)
		}
	}

	return nil
}

// PriorSyncEvent represents a prior synchronization event
type PriorSyncEvent struct {
	wal_types.BaseEvent
	Message   *priorsync.PriorSyncMessage `json:"-"` // Proto message
	ProtoData []byte                      `json:"proto_data"`
}

func (e *PriorSyncEvent) GetType() wal_types.WALType {
	return wal_types.PriorSync
}

func (e *PriorSyncEvent) GetOperation() wal_types.EventOperation {
	return e.Operation
}

func (e *PriorSyncEvent) Serialize() ([]byte, error) {
	e.Type = wal_types.PriorSync

	if e.Message != nil {
		protoBytes, err := proto.Marshal(e.Message)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal proto: %w", err)
		}
		e.ProtoData = protoBytes
	}

	return json.Marshal(e)
}

func (e *PriorSyncEvent) Deserialize(data []byte) error {
	if err := json.Unmarshal(data, e); err != nil {
		return err
	}

	if len(e.ProtoData) > 0 {
		e.Message = &priorsync.PriorSyncMessage{}
		if err := proto.Unmarshal(e.ProtoData, e.Message); err != nil {
			return fmt.Errorf("failed to unmarshal proto: %w", err)
		}
	}

	return nil
}

// DataSyncEvent represents a data synchronization event (non-header block data)
type DataSyncEvent struct {
	wal_types.BaseEvent
	Response  *datasync.DataSyncResponse `json:"-"` // Proto message
	ProtoData []byte                     `json:"proto_data"`
}

func (e *DataSyncEvent) GetType() wal_types.WALType {
	return wal_types.DataSync
}

func (e *DataSyncEvent) GetOperation() wal_types.EventOperation {
	return e.Operation
}

func (e *DataSyncEvent) Serialize() ([]byte, error) {
	e.Type = wal_types.DataSync

	if e.Response != nil {
		protoBytes, err := proto.Marshal(e.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal proto: %w", err)
		}
		e.ProtoData = protoBytes
	}

	return json.Marshal(e)
}

func (e *DataSyncEvent) Deserialize(data []byte) error {
	if err := json.Unmarshal(data, e); err != nil {
		return err
	}

	if len(e.ProtoData) > 0 {
		e.Response = &datasync.DataSyncResponse{}
		if err := proto.Unmarshal(e.ProtoData, e.Response); err != nil {
			return fmt.Errorf("failed to unmarshal proto: %w", err)
		}
	}

	return nil
}

// EventFactory creates the appropriate event adapter based on WAL type
func EventFactory(walType wal_types.WALType) (wal_types.EventAdapter, error) {
	switch walType {
	case wal_types.HeaderSync:
		return &HeaderSyncEvent{}, nil
	case wal_types.MerkleSync:
		return &MerkleSyncEvent{}, nil
	case wal_types.PriorSync:
		return &PriorSyncEvent{}, nil
	case wal_types.DataSync:
		return &DataSyncEvent{}, nil
	default:
		return nil, fmt.Errorf("unknown WAL type: %s", walType)
	}
}
