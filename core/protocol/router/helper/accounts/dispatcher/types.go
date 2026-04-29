package dispatcher

import (
	"context"
	"errors"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"sync"
	"sync/atomic"
	"time"

	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// noncePage is one dispatcher work item: a page of account nonces plus retry state.
type noncePage struct {
	Nonces  []uint64
	Retries int
}

// DispatcherConfig controls bounded-buffer behavior, worker fanout, and retry/timeout policy.
type DispatcherConfig struct {
	DispatchWorkers              int
	NonceBufferPauseThreshold    int64
	NonceBufferResumePct         int
	NoncePageSize                int
	DispatchMaxRetries           int
	DispatchACKTimeout           time.Duration
	DeadLetterCapacity           int
}

// DispatchPageMetrics captures per-page timings for DB fetch and network send paths.
type DispatchPageMetrics struct {
	PageIndex  uint32
	NonceCount int
	DBFetchMS  int64
	SendMS     int64
	Retries    int
	Success    bool
}

// DeadLetterPage captures terminal failures that exhausted retries or could not be re-queued.
type DeadLetterPage struct {
	Nonces      []uint64
	RetryCount  int
	FailureHint string
}

// DispatchSummary reports end-of-run delivery outcomes for one dispatcher session.
type DispatchSummary struct {
	DeliveredPages          uint32
	DeliveredAccounts       uint64
	PermanentFailedPages    uint32
	PermanentFailedAccounts uint64
	DeadLetters             []DeadLetterPage
}

// DispatcherCallbacks decouples dispatcher logic from DB and network integration details.
type DispatcherCallbacks struct {
	FetchAccounts func(ctx context.Context, nonces []uint64) ([]*types.Account, error)
	SendPage      func(ctx context.Context, pageIndex uint32, accounts []*accountspb.Account) error
	OnPageMetrics func(ctx context.Context, metrics DispatchPageMetrics)
	OnDeadLetter  func(ctx context.Context, dead DeadLetterPage)
}

// AccountDispatcher is a session-scoped async dispatcher with zero shared global state.
type AccountDispatcher struct {
	cfg       DispatcherConfig
	callbacks DispatcherCallbacks

	nonceChan      chan noncePage
	deadLetterChan chan DeadLetterPage

	chanMu    sync.RWMutex
	closeOnce sync.Once
	closed    atomic.Bool

	pauseMu   sync.Mutex
	pauseCond *sync.Cond

	nonceBufferCount atomic.Int64
	pageSequence     atomic.Uint32

	deliveredPages    atomic.Uint32
	deliveredAccounts atomic.Uint64

	failedPages    atomic.Uint32
	failedAccounts atomic.Uint64
}

// normalizeDispatcherConfig validates explicit fields and fills missing values with safe defaults.
func normalizeDispatcherConfig(cfg DispatcherConfig) (DispatcherConfig, error) {
	if cfg.DispatchWorkers <= 0 {
		cfg.DispatchWorkers = constants.DispatchWorkers // 10
	}
	if cfg.NonceBufferPauseThreshold <= 0 {
		cfg.NonceBufferPauseThreshold = constants.NonceBufferPauseThreshold // 100000
	}
	if cfg.NoncePageSize <= 0 {
		cfg.NoncePageSize = constants.NoncePageSize // 3000
	}
	if cfg.DispatchMaxRetries < 0 {
		cfg.DispatchMaxRetries = constants.DispatchMaxRetries // 3
	}
	if cfg.DispatchACKTimeout <= 0 {
		cfg.DispatchACKTimeout = constants.DispatchACKTimeout // 10 seconds
	}
	if cfg.DeadLetterCapacity <= 0 {
		cfg.DeadLetterCapacity = constants.DeadLetterCapacity // 2
	}
	if constants.NonceBufferResumePct >= 100 || constants.NonceBufferResumePct <= 0 {
		return DispatcherConfig{}, errors.New("dispatcher: NonceBufferResumePct constant must be > 0 and < 100")
	}
	if cfg.NoncePageSize <= 0 {
		return DispatcherConfig{}, errors.New("dispatcher: page size must be > 0")
	}
	return cfg, nil
}
