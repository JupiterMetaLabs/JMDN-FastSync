package types

import (
	"context"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
)

type PoTS struct {
	ctx context.Context
	// SyncStartTime is the moment FastSync began — used as the timestamp anchor
	// sent to the server in Phase 1 so it can resolve the nearest block.
	SyncStartTime time.Time

	// WAL is the dedicated PoTS WAL instance (separate from the FastSync WAL).
	WAL *WAL.WAL

	// ProtocolVersion is the negotiated wire protocol version, forwarded to the
	// communication layer when opening streams.
	ProtocolVersion uint16
}

func NewPoTS() *PoTS {
	return &PoTS{}
}

// Set Functions
func (p *PoTS) SetContext(ctx context.Context) *PoTS {
	p.ctx = ctx
	return p
}

func (p *PoTS) SetSyncStartTime(syncStartTime time.Time) *PoTS {
	p.SyncStartTime = syncStartTime
	return p
}

func (p *PoTS) SetWAL(wal *WAL.WAL) *PoTS {
	p.WAL = wal
	return p
}

func (p *PoTS) SetProtocolVersion(protocolVersion uint16) *PoTS {
	p.ProtocolVersion = protocolVersion
	return p
}

// Get Functions
func (p *PoTS) GetContext() context.Context {
	return p.ctx
}

func (p *PoTS) GetSyncStartTime() time.Time {
	return p.SyncStartTime
}

func (p *PoTS) GetWAL() *WAL.WAL {
	return p.WAL
}

func (p *PoTS) GetProtocolVersion() uint16 {
	return p.ProtocolVersion
}
