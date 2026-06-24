package reconsillation

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// Reconciliation_router defines the interface for the reconciliation module.
// It handles balance recalculation for tagged accounts after DataSync.
type Reconciliation_router interface {
	// SetSyncVars initializes the reconciliation module with node info and database access.
	// Must be called before Reconcile.
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, wal *WAL.WAL) Reconciliation_router

	// SetSyncVarsConfig sets the sync configuration.
	SetSyncVarsConfig(ctx context.Context, config types.Syncvars) Reconciliation_router

	// GetSyncVars returns the current sync configuration.
	GetSyncVars() *types.Syncvars

	// Reconcile calculates updated balances for all tagged accounts by querying
	// transactions in [fromBlock, toBlock] and adding the delta to each account's
	// current DB balance (delta-only approach — safe for repeated sync runs).
	// Pass math.MaxUint64 for toBlock to mean "up to latest block in DB."
	// Returns the number of accounts successfully reconciled and list of failed accounts.
	Reconcile(taggedAccounts *tagging.TaggedAccounts, remote *availabilitypb.AvailabilityResponse, fromBlock, toBlock uint64) (int, []string, error)

	// Close releases resources and cleans up.
	Close()
}
