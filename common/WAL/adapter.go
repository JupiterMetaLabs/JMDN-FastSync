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

// ReconciliationEvent represents an account reconciliation event
type ReconciliationEvent struct {
	wal_types.BaseEvent
	AccountAddress string `json:"account_address"`
	OldBalance     string `json:"old_balance"`
	NewBalance     string `json:"new_balance"`
	Nonce          uint64 `json:"nonce"`
	Timestamp      int64  `json:"timestamp"`
}

// GetType returns the WAL type for reconciliation events
func (e *ReconciliationEvent) GetType() wal_types.WALType {
	return wal_types.Reconciliation
}

// GetOperation returns the event operation type
func (e *ReconciliationEvent) GetOperation() wal_types.EventOperation {
	return wal_types.OpUpdate
}

// Serialize converts the event to bytes for storage
func (e *ReconciliationEvent) Serialize() ([]byte, error) {
	return json.Marshal(e)
}

// Deserialize reconstructs the event from bytes
func (e *ReconciliationEvent) Deserialize(data []byte) error {
	return json.Unmarshal(data, e)
}

// ReconciliationBatchEntry is a single account record inside a batch WAL event.
type ReconciliationBatchEntry struct {
	AccountAddress string `json:"a"`
	NewBalance     string `json:"b"`
	Nonce          uint64 `json:"n"`
}

// ReconciliationBatchEvent writes all reconciled accounts as a single WAL entry.
// This is far more efficient than writing one ReconciliationEvent per account when
// the account count is in the hundreds of thousands.
type ReconciliationBatchEvent struct {
	wal_types.BaseEvent
	Accounts  []ReconciliationBatchEntry `json:"accounts"`
	Timestamp int64                      `json:"timestamp"`
}

func (e *ReconciliationBatchEvent) GetType() wal_types.WALType {
	return wal_types.Reconciliation
}

func (e *ReconciliationBatchEvent) GetOperation() wal_types.EventOperation {
	return wal_types.OpUpdate
}

func (e *ReconciliationBatchEvent) Serialize() ([]byte, error) {
	e.Type = wal_types.Reconciliation
	return json.Marshal(e)
}

func (e *ReconciliationBatchEvent) Deserialize(data []byte) error {
	return json.Unmarshal(data, e)
}

// PoTSBlockEvent stores a single finalised block received during the PoTS collection
// window. BlockData holds the JSON-encoded ZKBlock so this package stays free of
// a dependency on common/types (which already imports common/WAL).
type PoTSBlockEvent struct {
	wal_types.BaseEvent
	BlockNumber uint64 `json:"block_number"` // denormalised for fast index rebuilds
	BlockData   []byte `json:"block_data"`   // JSON-encoded types.ZKBlock
	Timestamp   int64  `json:"timestamp"`
}

func (e *PoTSBlockEvent) GetType() wal_types.WALType { return wal_types.PoTS }

func (e *PoTSBlockEvent) GetOperation() wal_types.EventOperation { return e.Operation }

func (e *PoTSBlockEvent) Serialize() ([]byte, error) {
	e.Type = wal_types.PoTS
	return json.Marshal(e)
}

func (e *PoTSBlockEvent) Deserialize(data []byte) error {
	return json.Unmarshal(data, e)
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
	case wal_types.Reconciliation:
		return &ReconciliationBatchEvent{}, nil
	case wal_types.PoTS:
		return &PoTSBlockEvent{}, nil
	default:
		return nil, fmt.Errorf("unknown WAL type: %s", walType)
	}
}
