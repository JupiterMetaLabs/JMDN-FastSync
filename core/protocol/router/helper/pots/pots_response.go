package potshelper

import (
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	tagpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
)

type potsresponse_builder struct {
	PoTSResponse *potspb.PoTSResponse
}

func NewPoTSResponseBuilder() *potsresponse_builder {
	return &potsresponse_builder{
		PoTSResponse: &potspb.PoTSResponse{
			Phase: &phasepb.Phase{
				PresentPhase: constants.PoTS_RESPONSE,
			},
		},
	}
}

func (p *potsresponse_builder) AddTag(tags *tagpb.Tag) *potsresponse_builder {
	p.PoTSResponse.Tag = tags
	return p
}

func (p *potsresponse_builder) AddStatus(status bool) *potsresponse_builder {
	p.PoTSResponse.Phase.Success = status
	return p
}

func (p *potsresponse_builder) AddErrorMessage(message string) *potsresponse_builder {
	p.PoTSResponse.Phase.Error = message
	return p
}

func (p *potsresponse_builder) AddLatestBlockNumber(latestBlockNumber uint64) *potsresponse_builder {
	p.PoTSResponse.LatestBlockNumber = latestBlockNumber
	return p
}

func (p *potsresponse_builder) AddAuth(auth *authpb.Auth) *potsresponse_builder {
	p.PoTSResponse.Phase.Auth = auth
	return p
}

func (p *potsresponse_builder) AddSuccesivePhase(phase string) *potsresponse_builder {
	p.PoTSResponse.Phase.SuccessivePhase = phase
	return p
}

func (p *potsresponse_builder) Build() *potspb.PoTSResponse {
	return p.PoTSResponse
}