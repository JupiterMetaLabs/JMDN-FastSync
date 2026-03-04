package example

import (
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
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

type example_writeheaders struct{}

func (e example_blockinfo) NewHeadersWriter() types.WriteHeaders {
	return example_writeheaders{}
}

func (e example_writeheaders) WriteHeaders(headers []*block.Header) error {
	return nil
}

type example_writedata struct{}

func (e example_blockinfo) NewDataWriter() types.WriteData {
	return example_writedata{}
}

func (e example_writedata) WriteData(data []*block.NonHeaders) error {
	return nil
}	

type example_blocknonheader struct{}

func (e example_blockinfo) NewBlockNonHeaderIterator() types.BlockNonHeader {
	return example_blocknonheader{}
}

func (e example_blocknonheader) GetBlockNonHeaders(blocknumbers []uint64) ([]*block.NonHeaders, error) {
	return nil, nil
}

func (e example_blocknonheader) GetBlockNonHeadersRange(start, end uint64) ([]*block.NonHeaders, error) {
	return nil, nil
}


func GetBlockDetailsDummy() types.PriorSync {
	data := types.PriorSync{
		Blocknumber: 100,
		Stateroot:   []byte("example-stateroot"),
		Blockhash:   []byte("example-blockhash"),
		Metadata: types.Metadata{
			Version: 1,
		},
	}
	return data
}
