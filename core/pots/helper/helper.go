package helper

type PoTS_Helper struct{
	MAP map[uint64][]byte
}

func NewPoTS_Helper() *PoTS_Helper {
	return &PoTS_Helper{
		MAP: make(map[uint64][]byte),
	}
}

func (p *PoTS_Helper) AddBlock(blockNumber uint64, blockHash []byte) *PoTS_Helper {
	p.MAP[blockNumber] = blockHash
	return p
}

func (p *PoTS_Helper) AddBlockBatch(blockNumbers []uint64, blockHashes [][]byte) *PoTS_Helper {
	for i, blockNumber := range blockNumbers {
		p.MAP[blockNumber] = blockHashes[i]
	}
	return p
}

func (p *PoTS_Helper) RemoveBlock(blockNumber uint64) *PoTS_Helper {
	delete(p.MAP, blockNumber)
	return p
}

func (p *PoTS_Helper) Clear() *PoTS_Helper {
	p.MAP = make(map[uint64][]byte)
	return p
}

func (p *PoTS_Helper) GetMap() map[uint64][]byte {
	return p.MAP
}
