package types

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/merkle"
	"github.com/libp2p/go-libp2p/core/protocol"
)

type PriorSync struct {
	Blocknumber uint64
	Stateroot   []byte
	Blockhash   []byte
	MerkleTree  *merkle.MerkleTreeSnapshot
	Metadata    Metadata
}

type Metadata struct {
	Checksum []byte
	State    string
	Version  uint16
}

type PriorSyncAck struct {
	State string
	Ok    bool
	Error string
}

type PriorSyncMessage struct {
	Priorsync *PriorSync
	Ack       *PriorSyncAck
}

type Syncvars struct {
	Ctx      context.Context
	Protocol protocol.ID
	Version  uint16
	NodeInfo Nodeinfo
}
