package availability

import (
	"context"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
)

type Availability_router interface {
	// SendAvailabilityRequest sends an AvailabilityRequest to a peer and returns AvailabilityResponse
	SendAvailabilityRequest(ctx context.Context, syncVars *types.Syncvars, peerNode types.Nodeinfo, startBlock, endBlock uint64) (*availabilitypb.AvailabilityResponse, error)
	
	// SetSyncVars sets the sync variables for the availability router
	SetSyncVars(syncVars *types.Syncvars)
}
