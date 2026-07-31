package catchup

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

// CatchUp_router drives the catch-up sync that reconciles blocks from
// a known local tip (fromBlock) to the current chain tip on remote peers.
//
// Intended use: after a bootstrap snapshot has loaded blocks [0..X] into
// the local DB, call Run(ctx, X+1, peers) to pull [X+1..remoteTip] and
// replay all account balances.
//
// Phase order:
//  1. Availability probe → get auth tokens, discover remoteTip
//  2. HeaderSync (syncConfirmation=false) → fetch headers [fromBlock..remoteTip]
//  3. DataSync → fetch block bodies [fromBlock..remoteTip]
//  4. AccountSync → sync zero-tx accounts missed by DataSync tagging
//  5. Reconciliation → replay txs, compute final balances
//  6. PoTS gap fill → catch blocks produced while phases 2-5 were running
type CatchUp_router interface {
	// SetSyncVars initialises the catch-up module. Must be called before Run.
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) CatchUp_router

	// Run executes phases 1-6 against the supplied remote peers.
	// fromBlock is the first block not present in the local DB (bootstrap tip + 1).
	// peers is the list of candidate remote nodes; at least one must be reachable.
	// Returns when the node is fully caught up and PoTS gaps are filled.
	Run(ctx context.Context, fromBlock uint64, peers []types.Nodeinfo) error

	// GetSyncVars returns the current sync configuration.
	GetSyncVars() *types.Syncvars

	// Close releases resources.
	Close()
}
