package availability

import (
	"context"
	"errors"
	"sync"

	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	merklepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
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
	if syncVars == nil {
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

func (a *Availability) SendMultipleAvailabilityRequest(ctx context.Context, syncVars *types.Syncvars, peerNode []types.Nodeinfo, startBlock, endBlock uint64) ([]*availabilitypb.AvailabilityResponse, error) {
	if syncVars == nil {
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

	numNodes := len(peerNode)
	switch numNodes {
	case 0:
		return nil, nil
	case 1:
		data, err := a.SendAvailabilityRequest(ctx, syncVars, peerNode[0], startBlock, endBlock)
		if err != nil {
			return nil, err
		}
		return []*availabilitypb.AvailabilityResponse{data}, nil
	default:
		// fall through to parallel processing
	}

	parallelCount := constants.MAX_PARALLEL_REQUESTS
	parallelCount = min(parallelCount, numNodes)

	peerCh := make(chan types.Nodeinfo, numNodes)
	for _, peer := range peerNode {
		peerCh <- peer
	}
	close(peerCh)

	respCh := make(chan *availabilitypb.AvailabilityResponse, numNodes)
	var wg sync.WaitGroup

	for range parallelCount {
		wg.Go(func() {
			for peer := range peerCh {
				availabilityResponse, err := a.Comm.SendAvailabilityRequest(ctx, peer, availabilityRequest)
				if err == nil && availabilityResponse != nil && availabilityResponse.IsAvailable {
					respCh <- availabilityResponse
				}
			}
		})
	}

	wg.Wait()
	close(respCh)

	var responses []*availabilitypb.AvailabilityResponse
	for resp := range respCh {
		responses = append(responses, resp)
	}

	return responses, nil
}
