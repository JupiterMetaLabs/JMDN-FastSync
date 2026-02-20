package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// ZKBlock represents a block processed by the ZKVM with proof
type Header struct {
	// ZK-Stark proof data
	ProofHash  string   `json:"proof_hash"`
	Status     string   `json:"status"`
	TxnsRoot   string   `json:"txnsroot"`

	// Block data
	Timestamp    int64           `json:"timestamp"`
	ExtraData    string          `json:"extradata"`
	StateRoot    common.Hash     `json:"stateroot"`
	CoinbaseAddr *common.Address `json:"coinbaseaddr"`
	ZKVMAddr     *common.Address `json:"zkvmaddr"`
	PrevHash     common.Hash     `json:"prevhash"`
	BlockHash    common.Hash     `json:"blockhash"`
	GasLimit     uint64          `json:"gaslimit"`
	GasUsed      uint64          `json:"gasused"`
	BlockNumber  uint64          `json:"blocknumber"`
}

// ZKBlockTransaction represents a single transaction in a ZK block
type Transaction struct {
	Hash      common.Hash     `json:"hash"`               // 0x-prefixed 32-byte
	From      *common.Address `json:"from"`               // 0x-prefixed 20-byte
	To        *common.Address `json:"to,omitempty"`       // nil => contract creation
	Value     *big.Int        `json:"value"`              // big.Int as hex
	Type      uint8           `json:"type"`               // 0x0=Legacy, 0x1=AccessList, 0x2=DynamicFee
	Timestamp uint64          `json:"timestamp"`          // seconds since epoch (if you keep it)
	ChainID   *big.Int        `json:"chain_id,omitempty"` // present for 2930/1559 (and signed legacy w/155)
	Nonce     uint64          `json:"nonce"`
	GasLimit  uint64          `json:"gas_limit"` //TODO: Make it big int

	// Fee fields (use one set depending on Type)
	GasPrice       *big.Int `json:"gas_price,omitempty"`        // Legacy/EIP-2930
	MaxFee         *big.Int `json:"max_fee,omitempty"`          // 1559: maxFeePerGas
	MaxPriorityFee *big.Int `json:"max_priority_fee,omitempty"` // 1559: maxPriorityFeePerGas

	Data       []byte     `json:"data,omitempty"` // input
	AccessList AccessList `json:"access_list,omitempty"`

	// Signature (present once signed)
	V *big.Int `json:"v,omitempty"`
	R *big.Int `json:"r,omitempty"`
	S *big.Int `json:"s,omitempty"`
}

// ZKBlock represents a block processed by the ZKVM with proof
type ZKBlock struct {
	// ZK-Stark proof data
	StarkProof []byte   `json:"starkproof"`
	Commitment []uint32 `json:"commitment"`
	ProofHash  string   `json:"proof_hash"`
	Status     string   `json:"status"`
	TxnsRoot   string   `json:"txnsroot"`

	// Block data
	Transactions []Transaction   `json:"transactions"`
	Timestamp    int64           `json:"timestamp"`
	ExtraData    string          `json:"extradata"`
	StateRoot    common.Hash     `json:"stateroot"`
	LogsBloom    []byte          `json:"logsbloom"`
	CoinbaseAddr *common.Address `json:"coinbaseaddr"`
	ZKVMAddr     *common.Address `json:"zkvmaddr"`
	PrevHash     common.Hash     `json:"prevhash"`
	BlockHash    common.Hash     `json:"blockhash"`
	GasLimit     uint64          `json:"gaslimit"`
	GasUsed      uint64          `json:"gasused"`
	BlockNumber  uint64          `json:"blocknumber"`
}

// AccessTuple is the element type of an access list.
type AccessTuple struct {
	Address     common.Address
	StorageKeys []common.Hash
}

// AccessList is an EIP-2930 access list.
type AccessList []AccessTuple
