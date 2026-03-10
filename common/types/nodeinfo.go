package types

import (
	"time"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
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

type AUTHHandler interface{
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
