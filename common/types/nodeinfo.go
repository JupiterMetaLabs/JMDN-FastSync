package types

import (
	"math/big"
	"time"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
)

/*
- If version is not set, it will be default set to 1
V1: Supports only TCP connections.
V2: supports both TCP and QUIC connections. Prioritize QUIC connections, fallback to TCP if QUIC fails.
*/
type Nodeinfo struct {
	PeerID       peer.ID
	Multiaddr    []multiaddr.Multiaddr
	Capabilities []string
	PublicKey    []byte
	Version      uint16
	Protocol     protocol.ID
	BlockInfo    BlockInfo
	ART          *art.SwappableART
}

type AUTHStructure struct {
	UUID string
	TTL  time.Time
}

type BlockInfo interface {
	AUTH() AUTHHandler
	GetBlockNumber() uint64
	GetBlockDetails() PriorSync
	NewBlockIterator(start, end uint64, batchsize int) BlockIterator
	NewBlockHeaderIterator() BlockHeader
	NewBlockNonHeaderIterator() BlockNonHeader
	NewHeadersWriter() WriteHeaders
	NewDataWriter() WriteData
	NewAccountManager() AccountManager
	// NewAccountNonceIterator returns an iterator over all server accounts,
	// used during AccountSync diff computation to find accounts the client is missing.
	NewAccountNonceIterator(batchSize int) AccountNonceIterator
}

// AccountNonceIterator pages through all accounts stored on the server node.
// Used by AccountSync server-side to iterate every account and check whether
// the client already has each one (via SwappableART nonce lookup).
//
// Contract: TotalAccounts MUST be position-independent (e.g. a COUNT(*) query).
// Calling TotalAccounts before the first NextBatch MUST NOT advance the cursor.
// Implementations that violate this will cause ComputeAccountDiff to iterate
// from a non-zero offset and silently return an incomplete Missing set.
type AccountNonceIterator interface {
	// NextBatch returns the next batch of accounts. Returns nil slice and nil error at end.
	NextBatch() ([]*Account, error)
	// TotalAccounts returns the total number of accounts on the server.
	// Must be safe to call before the first NextBatch without affecting iteration order.
	TotalAccounts() (uint64, error)
	// Close releases any resources held by the iterator.
	Close()
}

type BlockIterator interface {
	Next() ([]*ZKBlock, error)
	Prev() ([]*ZKBlock, error)
	Close()
}

type BlockHeader interface {
	GetBlockHeaders(blocknumbers []uint64) ([]*blockpb.Header, error)
	GetBlockHeadersRange(start, end uint64) ([]*blockpb.Header, error)
}

type BlockNonHeader interface {
	GetBlockNonHeaders(blocknumbers []uint64) ([]*blockpb.NonHeaders, error)
	GetBlockNonHeadersRange(start, end uint64) ([]*blockpb.NonHeaders, error)
}

type WriteHeaders interface {
	WriteHeaders(headers []*blockpb.Header) error
}

type WriteData interface {
	WriteData(data []*blockpb.NonHeaders) error
}

// AccountUpdate describes a single account balance/nonce change for atomic batch commits.
type AccountUpdate struct {
	Address      string
	NewBalance   *big.Int
	Nonce        uint64
	IsNewAccount bool // true = CreateAccount, false = UpdateAccountBalance
}

// AccountManager handles account balance operations for reconciliation.
type AccountManager interface {
	// GetTransactionsForAccount retrieves all transactions where the account is sender or receiver.
	GetTransactionsForAccount(accountAddress string) ([]DBTransaction, error)

	// GetAccountBalance retrieves the current balance and nonce for an account.
	GetAccountBalance(accountAddress string) (*big.Int, uint64, error)

	// UpdateAccountBalance updates the balance and nonce for an existing account.
	UpdateAccountBalance(accountAddress string, balance *big.Int, nonce uint64) error

	// CreateAccount creates a new account record with the given balance and nonce.
	CreateAccount(accountAddress string, balance *big.Int, nonce uint64) error

	// BatchUpdateAccounts atomically applies all account updates in a single DB transaction.
	// Either every update is committed or none are (full rollback on any failure).
	BatchUpdateAccounts(updates []AccountUpdate) error
}

type AUTHHandler interface {
	/*
	   This will add record to the cache table,
	   also if already exist then it is designed to reset the timer for that record
	*/
	AddRecord(PeerID peer.ID, UUID string) error

	/*
	   This will remove record from the cache table
	*/
	RemoveRecord(PeerID peer.ID) error

	/*
	   This will get record from the cache table
	*/
	GetRecord(PeerID peer.ID) (AUTHStructure, error)

	/*
	   This will check if the record is present in the cache table [PeerID : UUID] with valid TTL
	*/
	IsAUTH(PeerID peer.ID, UUID string) (bool, error)

	/*
		Reset TTL for the record
	*/
	ResetTTL(PeerID peer.ID) error
}
