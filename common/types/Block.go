package types

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// ZKBlock represents a block processed by the ZKVM with proof
type Header struct {
	// ZK-Stark proof data
	ProofHash string `json:"proof_hash"`
	Status    string `json:"status"`
	TxnsRoot  string `json:"txnsroot"`

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

// SnapshotRecord maps to the snapshots table (append-only, Create Read).
// FK -> blocks.block_number (one-to-one).
type SnapshotRecord struct {
	BlockHash common.Hash `json:"block_hash"`
	CreatedAt time.Time   `json:"created_at"`
}

// ZKProof maps to the zk_proofs table (append-only, Create Read).
// FK -> blocks.block_number (one-proof-per-block).
type ZKProof struct {
	ProofHash  string    `json:"proof_hash"`
	StarkProof []byte    `json:"stark_proof"`
	Commitment []byte    `json:"commitment"`
	CreatedAt  time.Time `json:"created_at"`
}

// DBTransaction maps to the transactions table (append-only, Create Read).
// FK -> snapshots.block_number (snapshot owns the tx set).
// Embeds Transaction for all core tx fields; adds only the DB-specific extras.
type DBTransaction struct {
	Transaction           // all core fields (Hash, From, To, Value, Gas, Sig, etc.)
	BlockNumber uint64    `json:"block_number"` // The block this transaction belongs to
	TxIndex     uint16    `json:"tx_index"`     // SMALLINT position within the block
	CreatedAt   time.Time `json:"created_at"`   // TIMESTAMPTZ insert time
}

// L1Finality maps to the l1_finality table (append-only, Create Read).
// Confirmation is the L1 anchor address (e.g. settlement tx hash / contract addr).
type L1Finality struct {
	Confirmation common.Address         `json:"confirmation"`
	BlockNumbers []uint64               `json:"block_numbers"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
}

// NonHeaders aggregates all data owned by a block except the blocks table
// header fields (state_root, parent_hash, gas, timestamps, etc.).
// BlockNumber is the shared primary key / foreign key across all sub-tables.
type NonHeaders struct {
	BlockNumber  uint64          `json:"block_number"`
	Snapshot     SnapshotRecord  `json:"snapshot"`
	Transactions []DBTransaction `json:"transactions"`
	ZKProof      ZKProof         `json:"zk_proof"`
	L1Finality   *L1Finality     `json:"l1_finality,omitempty"` // nil until finalised on L1
}

// This will be stored in the DB
type Account struct {
	// Legacy DID fields (for backward compatibility)
	DIDAddress string `json:"did,omitempty"`

	// New PublicKey based fields
	Address common.Address `json:"address"` // Derived from PublicKey
	Balance string         `json:"balance,omitempty"`
	Nonce   uint64         `json:"nonce"`

	// Account metadata
	AccountType string `json:"account_type"` // "did" or "publickey"
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`

	// Optional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}