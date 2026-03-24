package helper

import (
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	potspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/pots"
)

type potsrequest_builder struct {
	PoTSRequest *potspb.PoTSRequest
}

func NewPoTSRequestBuilder() *potsrequest_builder {
	return &potsrequest_builder{
		PoTSRequest: &potspb.PoTSRequest{},
	}
}

func (p *potsrequest_builder) AddMap(MAP map[uint64][]byte) *potsrequest_builder {
	p.PoTSRequest.Blocks = MAP
	return p
}

func (p *potsrequest_builder) AddBlock(blockNumber uint64, blockHash []byte) *potsrequest_builder {
	p.PoTSRequest.Blocks[blockNumber] = blockHash
	return p
}

func (p *potsrequest_builder) AddLatestBlock(blocknumber uint64) *potsrequest_builder {
	p.PoTSRequest.LatestBlockNumber = blocknumber
	return p
}

func (p *potsrequest_builder) AddAuth(auth *authpb.Auth) *potsrequest_builder {
	p.PoTSRequest.Auth = auth
	return p
}

func (p *potsrequest_builder) Build() *potspb.PoTSRequest {
	return p.PoTSRequest
}
