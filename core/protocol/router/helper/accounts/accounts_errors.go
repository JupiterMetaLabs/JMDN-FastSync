package accounts

import (
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
)

// resultFactory builds AccountsSyncResult values for every outcome the stream
// handler needs to produce. Callers use the factory methods instead of
// constructing results inline so that the wire shape is defined in one place.
//
// Usage:
//
//	f := NewResultFactory(batchIndex)
//	return f.ErrEndOfStream("something went wrong")
//	return f.BatchAck()
//	return f.DiffReady(missing)
type resultFactory struct {
	batchIndex uint32
}

// NewResultFactory returns a factory scoped to the given batch index.
func NewResultFactory(batchIndex uint32) *resultFactory {
	return &resultFactory{batchIndex: batchIndex}
}

// ErrEndOfStream returns a result that tells the stream handler to send an
// EndOfStream with Ack.Ok=false and close the stream.
// Use this for every error path: auth failure, checksum mismatch, merge error, etc.
func (f *resultFactory) ErrEndOfStream(msg string) *AccountsSyncResult {
	return &AccountsSyncResult{
		EndOfStream: &accountspb.AccountSyncEndOfStream{
			Ack: &ackpb.Ack{Ok: false, Error: msg},
			Phase: &phasepb.Phase{
				PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
				SuccessivePhase: constants.FAILURE,
				Success:         false,
				Error:           msg,
			},
		},
	}
}

// BatchAck returns a non-terminal result that tells the stream handler to send
// an AccountBatchAck and loop for the next chunk (is_last=false path).
func (f *resultFactory) BatchAck() *AccountsSyncResult {
	return &AccountsSyncResult{
		BatchAck: &accountspb.AccountBatchAck{
			BatchIndex: f.batchIndex,
			Ack:        &ackpb.Ack{Ok: true},
		},
	}
}

// DiffReady returns the final success result (is_last=true path).
// Missing holds every proto account the client lacks; the stream handler will
// page them to the client and then send EndOfStream.
func (f *resultFactory) DiffReady(missing []*accountspb.Account) *AccountsSyncResult {
	return &AccountsSyncResult{
		BatchAck: &accountspb.AccountBatchAck{
			BatchIndex: f.batchIndex,
			Ack:        &ackpb.Ack{Ok: true},
		},
		Missing:   missing,
		DiffReady: true,
	}
}
