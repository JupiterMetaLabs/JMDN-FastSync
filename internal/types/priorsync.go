package types

import (
	"context"

	"github.com/libp2p/go-libp2p/core/protocol"
)

type PriorSync struct {
	Blocknumber uint64
	Stateroot   []byte
	Blockhash   []byte
	Metadata    Metadata
}

type Metadata struct {
	Checksum []byte
	State    string
	Version  uint16
}

type Syncvars struct {
	Ctx      context.Context
	Protocol protocol.ID
	Version  uint16
	NodeInfo Nodeinfo
}
