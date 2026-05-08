package accountsync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

// AccountSync_router is the client-side interface for Phase 5 (AccountSync).
//
// AccountSync syncs zero-transaction accounts that were not covered by the
// DataSync TaggedAccounts — accounts that never appeared in any block but
// exist on the server.
//
// Protocol flow (client perspective):
//  1. Iterate all local account nonces and split into ART chunks.
//  2. Upload each chunk to the server on AccountsSyncProtocol.
//     Non-final chunks: send → wait for BatchAck → send next.
//  3. Send the final chunk (is_last=true) and hold the stream open.
//  4. Server diffs the merged ART against its own accounts and streams
//     missing accounts back via dial-back on AccountsSyncDataProtocol.
//     Each dial-back page is received by the HandleAccountsSyncData handler
//     registered on the host — no action required from the caller.
//  5. Server sends AccountSyncEndOfStream on the original stream once all
//     pages have been delivered and acked. AccountSync returns at this point.
//
// By the time AccountSync returns, all missing accounts are written to the
// local DB and available for the Reconciliation phase.
type AccountSync_router interface {
	// SetSyncVars initialises the router with the node's identity, host, and
	// WAL. Must be called before AccountSync.
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) AccountSync_router

	// GetSyncVars returns the current sync vars — useful for other modules
	// that need to inspect the node identity or WAL reference.
	GetSyncVars() *types.Syncvars

	// This will start listening for the account sync data from the server. 
	// This is used to handle the account sync data from the server.
	// startAccountSyncData(ctx context.Context) accountSyncTypes.AccountSyncData_router

	// AccountSync runs the full Phase 5 client flow against a single server
	// identified by the AvailabilityResponse (carries Nodeinfo + Auth UUID).
	//
	// Returns the total number of missing accounts received from the server
	// as reported in the EndOfStream frame. Returns an error if the upload,
	// diff, or stream delivery fails.
	AccountSync(remote *availabilitypb.AvailabilityResponse) (uint64, error)

	// Close releases resources held by the router.
	Close()
}


