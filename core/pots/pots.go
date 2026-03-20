package pots

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
)

const namedlogger = "log:pots"

// PoTS implements PoTS_router. One instance is created at FastSync T=0 and
// lives until the node transitions to live consensus.
type PoTS struct {
	SyncVars  *types.Syncvars
	PoTSState *types.PoTS
	PoTS_WAL  PoTS_WAL
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewPoTS returns a zero-value PoTS ready for SetSyncVars.
func NewPoTS() *PoTS {
	return &PoTS{}
}

// SetSyncVars initialises the PoTS module. SyncStartTime is captured here —
// the caller must invoke this at the exact moment FastSync begins.
func (p *PoTS) SetSyncVars(
	ctx context.Context,
	protocolVersion uint16,
	nodeInfo types.Nodeinfo,
) PoTS_router {
	ctx, cancel := context.WithCancel(ctx)

	p.mu.Lock()
	p.ctx = ctx
	p.cancel = cancel
	p.mu.Unlock()

	if p.SyncVars == nil {
		p.SyncVars = &types.Syncvars{}
	}
	p.SyncVars.Version = protocolVersion
	p.SyncVars.NodeInfo = nodeInfo
	p.SyncVars.Ctx = ctx

	p.PoTSState = &types.PoTS{
		SyncStartTime:   time.Now().UTC(),
		ProtocolVersion: protocolVersion,
	}

	Log.Logger(namedlogger).Info(ctx, "PoTS initialised",
		ion.String("sync_start_time", p.PoTSState.SyncStartTime.Format(time.RFC3339Nano)))

	return p
}

func (p *PoTS) StartPoTS(ctx context.Context, pots *types.PoTS) PoTS_router {
	if p.SyncVars == nil {
		return p
	}
	p.mu.Lock()
	p.PoTSState = pots
	p.mu.Unlock()
	return p
}

// SetWAL creates a dedicated PoTS WAL wrapper and returns a PoTS_WAL handle.
// The WAL is isolated from the main FastSync WAL — different directory, same
// implementation. Any blocks buffered during the FastSync window are stored here.
func (p *PoTS) SetWAL(ctx context.Context, wal *WAL.WAL) PoTS_router {
	if p.SyncVars == nil {
		return p
	}

	if p.PoTS_WAL != nil {
		return p
	}
	
	potsWAL, err := NewPoTSWAL(p.SyncVars.Ctx, wal)
	if err != nil {
		Log.Logger(namedlogger).Warn(p.SyncVars.Ctx, "Failed to create PoTS WAL",
			ion.String("dir", wal.Dir),
			ion.Err(err))
		return nil
	}

	// Set the WAL to the types.PoTSState
	// This is safe to do while holding the lock since the WAL is not being read from
	p.mu.Lock()
	p.PoTS_WAL = potsWAL
	p.mu.Unlock()

	Log.Logger(namedlogger).Info(p.SyncVars.Ctx, "PoTS WAL created",
		ion.String("dir", wal.Dir))

	return p
}

// GetWAL returns the PoTS WAL interface.
func (p *PoTS) GetWAL() (PoTS_WAL, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.PoTS_WAL == nil {
		return nil, errors.New("PoTS WAL not initialized")
	}
	
	return p.PoTS_WAL, nil
}

// GetSyncVars returns the current sync configuration.
func (p *PoTS) GetSyncVars() *types.Syncvars {
	return p.SyncVars
}

// GetPoTS returns the PoTS state (SyncStartTime, WAL, Auth, Node).
func (p *PoTS) GetPoTS() *types.PoTS {
	return p.PoTSState
}

// Close cancels the internal context and signals all goroutines to stop.
func (p *PoTS) Close() {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	Log.Logger(namedlogger).Info(context.Background(), "PoTS closed")
}

// Validate returns an error if the PoTS is not ready to operate.
func (p *PoTS) Validate() error {
	if p.SyncVars == nil {
		return fmt.Errorf("PoTS: SetSyncVars not called")
	}
	if p.PoTSState == nil {
		return fmt.Errorf("PoTS: state not initialised")
	}
	if p.SyncVars.Ctx == nil {
		return fmt.Errorf("PoTS: context is nil")
	}
	return nil
}

// ensure *PoTS satisfies the interface at compile time.
var _ PoTS_router = (*PoTS)(nil)

// PoTSWALDir returns the canonical directory for the PoTS WAL given the base
// data directory. Keeping this centralised avoids path mismatches.
func PoTSWALDir(baseDir string) string {
	return baseDir + "/pots"
}

// DefaultPoTSWALBatchSize is the buffer depth before the PoTS WAL auto-flushes.
// Blocks arrive at consensus cadence (~one per slot), so a small buffer is fine.
const DefaultPoTSWALBatchSize = wal_types.DefaultBatchSize
