package example

import (
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
)

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

func (e example_blockinfo) NewBlockIterator(start, end uint64, batchsize int) types.BlockIterator {
	return &example_iterator{}
}

type example_iterator struct{}

func (i *example_iterator) Next() ([]*types.ZKBlock, error) {
	return nil, nil
}

func (i *example_iterator) Prev() ([]*types.ZKBlock, error) {
	return nil, nil
}

func (i *example_iterator) Close() {}

type example_blockheader struct{}

func (e example_blockinfo) NewBlockHeaderIterator() types.BlockHeader {
	return example_blockheader{}
}

func (e example_blockheader) GetBlockHeaders(blocknumbers []uint64) ([]*block.Header, error) {
	return nil, nil
}

func (e example_blockheader) GetBlockHeadersRange(start, end uint64) ([]*block.Header, error) {
	return nil, nil
}

func GetBlockDetailsDummy() types.PriorSync {
	data := types.PriorSync{
		Blocknumber: 100,
		Stateroot:   []byte("example-stateroot"),
		Blockhash:   []byte("example-blockhash"),
		Metadata: types.Metadata{
			State:   "example-state",
			Version: 1,
		},
	}
	return data
}
