package availability

import (
	"context"
	"errors"

	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
)

type Availability struct {
	SyncVars *types.Syncvars
	Comm     communication.Communicator
}

func NewAvailability() Availability_router {
	return &Availability{}
}

func (a *Availability) SetSyncVars(syncVars *types.Syncvars) {
	a.SyncVars = syncVars
}

func (a *Availability) SendAvailabilityRequest(ctx context.Context, syncVars *types.Syncvars, peerNode types.Nodeinfo, startBlock, endBlock uint64) (*availabilitypb.AvailabilityResponse, error) {
	if syncVars == nil{
		return nil, errors.New("syncVars is nil")
	}

	a.Comm = communication.NewCommunication(a.SyncVars.Node, a.SyncVars.Version)

	Range := &merklepb.Range{
		Start: startBlock,
		End:   endBlock,
	}

	availabilityRequest := &availabilitypb.AvailabilityRequest{
		Range: Range,
	}

	availabilityResponse, err := a.Comm.SendAvailabilityRequest(ctx, peerNode, availabilityRequest)
	if err != nil {
		return nil, err
	}

	return availabilityResponse, nil
}
