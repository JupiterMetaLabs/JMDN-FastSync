package example

import "github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"

type example_blockinfo struct{}

func NewExampleBlockInfo() types.BlockInfo {
	return example_blockinfo{}
}

func (e example_blockinfo) GetBlockNumber() uint64 {
	return 100
}

func (e example_blockinfo) GetBlockDetails() types.PriorSync {
	return GetBlockDetailsDummy()
}


func GetBlockDetailsDummy() types.PriorSync {
	data := types.PriorSync{
		Blocknumber: 100,
		Stateroot:   []byte("example-stateroot"),
		Blockhash:   []byte("example-blockhash"),
		Metadata: types.Metadata{
			State:    "example-state",
			Version:  1,
		},
	}
	return data	
}