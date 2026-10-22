package reconsillation

import (
	"context"
	"math/big"
	"testing"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/ethereum/go-ethereum/common"
)

// headerOnlyBlockInfo serves block headers; every other BlockInfo method is
// unused here and panics through the nil embedded interface.
type headerOnlyBlockInfo struct {
	types.BlockInfo
	headers map[uint64]*blockpb.Header
	calls   int
}

func (h *headerOnlyBlockInfo) NewBlockHeaderIterator() types.BlockHeader { return h }

func (h *headerOnlyBlockInfo) GetBlockHeaders(nums []uint64) ([]*blockpb.Header, error) {
	h.calls++
	var out []*blockpb.Header
	for _, n := range nums {
		if hd, ok := h.headers[n]; ok {
			out = append(out, hd)
		}
	}
	return out, nil
}

func (h *headerOnlyBlockInfo) GetBlockHeadersRange(start, end uint64) ([]*blockpb.Header, error) {
	panic("unused in test")
}

// The coinbase and ZKVM fee credits depend on the block header. A
// Reconciliation built without SetLRUCache (as jmdn builds it) must still read
// the header and credit both shares instead of silently skipping them.
func TestFeeCreditWithoutHeaderCache(t *testing.T) {
	coinbase := common.HexToAddress("0x00000000000000000000000000000000000000c0")
	zkvm := common.HexToAddress("0x00000000000000000000000000000000000000d0")
	sender := common.HexToAddress("0x00000000000000000000000000000000000000a0")

	bi := &headerOnlyBlockInfo{headers: map[uint64]*blockpb.Header{
		7: {BlockNumber: 7, CoinbaseAddr: coinbase.Bytes(), ZkvmAddr: zkvm.Bytes()},
	}}
	r := NewReconciliation()
	r.SetSyncVarsConfig(context.Background(), types.Syncvars{NodeInfo: types.Nodeinfo{BlockInfo: bi}})

	// Odd fee so the remainder wei goes to the coinbase.
	tx := types.DBTransaction{BlockNumber: 7}
	tx.From = &sender
	tx.GasLimit = 21001
	tx.GasPrice = big.NewInt(3) // fee 63003 -> coinbase 31502, zkvm 31501

	cb := r.calculateAccountState(coinbase.Hex(), []types.DBTransaction{tx})
	if got, want := cb.ComputedBalance, big.NewInt(31502); got.Cmp(want) != 0 {
		t.Fatalf("coinbase credit = %s, want %s", got, want)
	}
	zk := r.calculateAccountState(zkvm.Hex(), []types.DBTransaction{tx})
	if got, want := zk.ComputedBalance, big.NewInt(31501); got.Cmp(want) != 0 {
		t.Fatalf("zkvm credit = %s, want %s", got, want)
	}
	if bi.calls == 0 {
		t.Fatal("header was never read from the DB")
	}
}
