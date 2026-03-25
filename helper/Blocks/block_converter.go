package blocks

import (
	"math/big"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

// protoToZKBlock converts a proto block.ZKBlock to a types.ZKBlock.
// The proto representation uses raw bytes for Ethereum types; this function
// converts them back to the correct Go types.
func ProtoToZKBlock(pb *blockpb.ZKBlock) *types.ZKBlock {
	b := &types.ZKBlock{
		StarkProof:  pb.GetStarkProof(),
		Commitment:  pb.GetCommitment(),
		ProofHash:   pb.GetProofHash(),
		Status:      pb.GetStatus(),
		TxnsRoot:    pb.GetTxnsRoot(),
		Timestamp:   pb.GetTimestamp(),
		ExtraData:   pb.GetExtraData(),
		LogsBloom:   pb.GetLogsBloom(),
		GasLimit:    pb.GetGasLimit(),
		GasUsed:     pb.GetGasUsed(),
		BlockNumber: pb.GetBlockNumber(),
		StateRoot:   ethcommon.BytesToHash(pb.GetStateRoot()),
		PrevHash:    ethcommon.BytesToHash(pb.GetPrevHash()),
		BlockHash:   ethcommon.BytesToHash(pb.GetBlockHash()),
	}
	if ca := pb.GetCoinbaseAddr(); len(ca) > 0 {
		addr := ethcommon.BytesToAddress(ca)
		b.CoinbaseAddr = &addr
	}
	if za := pb.GetZkvmAddr(); len(za) > 0 {
		addr := ethcommon.BytesToAddress(za)
		b.ZKVMAddr = &addr
	}
	b.Transactions = make([]types.Transaction, len(pb.GetTransactions()))
	for i, tx := range pb.GetTransactions() {
		b.Transactions[i] = protoToTransaction(tx)
	}
	return b
}


// protoToTransaction converts a proto block.Transaction to a types.Transaction.
func protoToTransaction(tx *blockpb.Transaction) types.Transaction {
	t := types.Transaction{
		Hash:           ethcommon.BytesToHash(tx.GetHash()),
		Type:           uint8(tx.GetType()),
		Timestamp:      tx.GetTimestamp(),
		Nonce:          tx.GetNonce(),
		GasLimit:       tx.GetGasLimit(),
		Data:           tx.GetData(),
		Value:          bytesToBigInt(tx.GetValue()),
		ChainID:        bytesToBigInt(tx.GetChainId()),
		GasPrice:       bytesToBigInt(tx.GetGasPrice()),
		MaxFee:         bytesToBigInt(tx.GetMaxFee()),
		MaxPriorityFee: bytesToBigInt(tx.GetMaxPriorityFee()),
		V:              bytesToBigInt(tx.GetV()),
		R:              bytesToBigInt(tx.GetR()),
		S:              bytesToBigInt(tx.GetS()),
	}
	if from := tx.GetFrom(); len(from) > 0 {
		addr := ethcommon.BytesToAddress(from)
		t.From = &addr
	}
	if to := tx.GetTo(); len(to) > 0 {
		addr := ethcommon.BytesToAddress(to)
		t.To = &addr
	}
	return t
}


// bytesToBigInt converts a big-endian byte slice to *big.Int.
// Returns nil for empty/nil slices (proto zero value).
func bytesToBigInt(b []byte) *big.Int {
	if len(b) == 0 {
		return nil
	}
	return new(big.Int).SetBytes(b)
}

// bigIntToBytes converts a *big.Int to a big-endian byte slice.
// Returns nil for nil values (proto zero value).
func bigIntToBytes(n *big.Int) []byte {
	if n == nil {
		return nil
	}
	return n.Bytes()
}

// ZKBlockToProto converts a types.ZKBlock to a proto block.ZKBlock.
func ZKBlockToProto(b *types.ZKBlock) *blockpb.ZKBlock {
	pb := &blockpb.ZKBlock{
		StarkProof:  b.StarkProof,
		Commitment:  b.Commitment,
		ProofHash:   b.ProofHash,
		Status:      b.Status,
		TxnsRoot:    b.TxnsRoot,
		Timestamp:   b.Timestamp,
		ExtraData:   b.ExtraData,
		StateRoot:   b.StateRoot.Bytes(),
		LogsBloom:   b.LogsBloom,
		PrevHash:    b.PrevHash.Bytes(),
		BlockHash:   b.BlockHash.Bytes(),
		GasLimit:    b.GasLimit,
		GasUsed:     b.GasUsed,
		BlockNumber: b.BlockNumber,
	}
	if b.CoinbaseAddr != nil {
		pb.CoinbaseAddr = b.CoinbaseAddr.Bytes()
	}
	if b.ZKVMAddr != nil {
		pb.ZkvmAddr = b.ZKVMAddr.Bytes()
	}
	pb.Transactions = make([]*blockpb.Transaction, len(b.Transactions))
	for i, tx := range b.Transactions {
		pb.Transactions[i] = transactionToProto(tx)
	}
	return pb
}

// transactionToProto converts a types.Transaction to a proto block.Transaction.
func transactionToProto(tx types.Transaction) *blockpb.Transaction {
	t := &blockpb.Transaction{
		Hash:           tx.Hash.Bytes(),
		Type:           uint32(tx.Type),
		Timestamp:      tx.Timestamp,
		Nonce:          tx.Nonce,
		GasLimit:       tx.GasLimit,
		Data:           tx.Data,
		Value:          bigIntToBytes(tx.Value),
		ChainId:        bigIntToBytes(tx.ChainID),
		GasPrice:       bigIntToBytes(tx.GasPrice),
		MaxFee:         bigIntToBytes(tx.MaxFee),
		MaxPriorityFee: bigIntToBytes(tx.MaxPriorityFee),
		V:              bigIntToBytes(tx.V),
		R:              bigIntToBytes(tx.R),
		S:              bigIntToBytes(tx.S),
	}
	if tx.From != nil {
		t.From = tx.From.Bytes()
	}
	if tx.To != nil {
		t.To = tx.To.Bytes()
	}
	return t
}