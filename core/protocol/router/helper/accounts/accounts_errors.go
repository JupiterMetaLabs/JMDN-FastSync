package accounts

import (
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
)

// resultFactory builds *accountspb.AccountSyncServerMessage values for every
// outcome the stream handler needs to produce. Construct one per request via
// NewResultFactory(batchIndex) so the batch index is captured once and reused
// across all factory calls for that request.
//
// Usage:
//
//	f := NewResultFactory(req.GetBatchIndex())
//	return f.ErrEndOfStream("something went wrong")  // error → terminate stream
//	return f.BatchAck()                              // non-final → continue loop
//	return f.DiffReady(missing)                      // final → dispatch pages
type resultFactory struct {
	batchIndex uint32
}

// NewResultFactory returns a factory scoped to the given batch index.
func NewResultFactory(batchIndex uint32) *resultFactory {
	return &resultFactory{batchIndex: batchIndex}
}

// ErrEndOfStream returns an AccountSyncServerMessage carrying an EndOfStream
// with Ack.Ok=false. The stream handler must send this and close the stream.
// Use for every error path: auth failure, checksum mismatch, merge error, etc.
func (f *resultFactory) ErrEndOfStream(msg string) *accountspb.AccountSyncServerMessage {
	return &accountspb.AccountSyncServerMessage{
		Payload: &accountspb.AccountSyncServerMessage_End{
			End: &accountspb.AccountSyncEndOfStream{
				Ack: &ackpb.Ack{Ok: false, Error: msg},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
					SuccessivePhase: constants.FAILURE,
					Success:         false,
					Error:           msg,
				},
			},
		},
	}
}

// BatchAck returns an AccountSyncServerMessage carrying a BatchAck for the
// non-final (is_last=false) path. Stream handler sends it and loops for the
// next chunk.
func (f *resultFactory) BatchAck() *accountspb.AccountSyncServerMessage {
	return &accountspb.AccountSyncServerMessage{
		Payload: &accountspb.AccountSyncServerMessage_BatchAck{
			BatchAck: &accountspb.AccountBatchAck{
				BatchIndex: f.batchIndex,
				Ack:        &ackpb.Ack{Ok: true},
			},
		},
	}
}

// DiffReady returns an AccountSyncServerMessage carrying an EndOfStream with
// Ack.Ok=true and TotalAccounts populated. This is the diff-ready signal for
// the final (is_last=true) success path.
//
// The stream handler inspects GetEnd().GetAck().Ok to distinguish this from an
// error EndOfStream, then calls router.TakePendingMissing() to retrieve the
// account slice and dispatch pages before sending its own final EndOfStream.
func (f *resultFactory) DiffReady(missing []*accountspb.Account) *accountspb.AccountSyncServerMessage {
	return &accountspb.AccountSyncServerMessage{
		Payload: &accountspb.AccountSyncServerMessage_End{
			End: &accountspb.AccountSyncEndOfStream{
				TotalAccounts: uint64(len(missing)),
				Ack:           &ackpb.Ack{Ok: true},
				Phase: &phasepb.Phase{
					PresentPhase:    constants.ACCOUNTS_SYNC_RESPONSE,
					SuccessivePhase: constants.ACCOUNTS_SYNC_RESPONSE,
					Success:         true,
				},
			},
		},
	}
}
