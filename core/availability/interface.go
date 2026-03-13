package availability

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Availability_router interface {
	// SendAvailabilityRequest sends an AvailabilityRequest to a peer and returns AvailabilityResponse
	SendAvailabilityRequest(ctx context.Context, syncVars *types.Syncvars, peerNode types.Nodeinfo, startBlock, endBlock uint64) (*availabilitypb.AvailabilityResponse, error)

	// SendAvailabilityRequest sends an AvailabilityRequest to a peer and returns AvailabilityResponse
	SendMultipleAvailabilityRequest(ctx context.Context, syncVars *types.Syncvars, peerNode []types.Nodeinfo, startBlock, endBlock uint64) ([]*availabilitypb.AvailabilityResponse, error)
	
	// SetSyncVars sets the sync variables for the availability router
	SetSyncVarsConfig(ctx context.Context, syncVars types.Syncvars)

	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) Availability_router

	Close()
}
