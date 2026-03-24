package potshelper

import (
	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
	tagpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
)

type potsresponse_builder struct {
	PoTSResponse *potspb.PoTSResponse
}

func NewPoTSResponseBuilder() *potsresponse_builder {
	return &potsresponse_builder{
		PoTSResponse: &potspb.PoTSResponse{},
	}
}

func (p *potsresponse_builder) AddTag(tags *tagpb.Tag) *potsresponse_builder {
	p.PoTSResponse.Tag = tags
	return p
}

func (p *potsresponse_builder) AddStatus(status bool) *potsresponse_builder {
	p.PoTSResponse.Success = status
	return p
}

func (p *potsresponse_builder) AddErrorMessage(message string) *potsresponse_builder {
	p.PoTSResponse.ErrorMessage = message
	return p
}

func (p *potsresponse_builder) AddLatestBlockNumber(latestBlockNumber uint64) *potsresponse_builder {
	p.PoTSResponse.LatestBlockNumber = latestBlockNumber
	return p
}

func (p *potsresponse_builder) Build() *potspb.PoTSResponse {
	return p.PoTSResponse
}