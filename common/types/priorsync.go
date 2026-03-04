package types

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	"github.com/JupiterMetaLabs/JMDN_Merkletree/merkletree"
	"github.com/libp2p/go-libp2p/core/host"
)

type Range struct {
	Start uint64
	End   uint64
}

type PriorSync struct {
	Blocknumber uint64
	Stateroot   []byte
	Blockhash   []byte
	MerkleTree  *merkletree.MerkleTreeSnapshot
	Range       *Range
	Metadata    Metadata
}

type Metadata struct {
	Checksum []byte
	Version  uint16
}

type PriorSyncAck struct {
	State string
	Ok    bool
	Error string
}

type PriorSyncMessage struct {
	Priorsync  *PriorSync
	Ack        *PriorSyncAck
	Phase      *Phase
	Headersync *headersyncpb.HeaderSyncRequest
}

type Syncvars struct {
	Ctx      context.Context
	Version  uint16
	NodeInfo Nodeinfo
	WAL      *WAL.WAL
	Node     host.Host
}

type Phase struct {
	PresentPhase    string
	SuccessivePhase string
	Success         bool
	Error           string
}
