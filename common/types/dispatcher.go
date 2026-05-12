package types

import (
	"context"
	"time"
)

// ─── AccountSync Dispatcher Types ─────────────────────────────────────────────
//
// These types define the configuration and observable outputs of the
// AccountSync async streaming dispatcher. They live in common/types so both
// the dispatcher implementation (core/protocol/router/helper/accounts) and
// the diff stage (accounts_tag.go) can reference them without import cycles.

// DispatcherConfig is fully self-describing. After construction, no code path
// reads the constants package — callers may override any field before passing
// to NewAccountDispatcher. Use DefaultDispatcherConfig() for sensible defaults.
type DispatcherConfig struct {
	// DispatchWorkers is the number of concurrent goroutines draining the
	// nonce channel. Each worker: DB fetch → convert → send page → wait ACK.
	DispatchWorkers int

	// NonceBufferPauseThreshold is the soft upper limit on nonces buffered in
	// the dispatcher. The diff stage pauses starting new batches when this is
	// reached. The buffer itself always accepts data regardless of this value.
	NonceBufferPauseThreshold int64

	// NonceBufferResumePct is the percentage of NonceBufferPauseThreshold at
	// which a paused diff stage is allowed to resume.
	// Resume threshold = NonceBufferPauseThreshold × NonceBufferResumePct / 100.
	NonceBufferResumePct int

	// NoncePageSize is the maximum number of nonces per dispatcher page.
	// The final page of a session is variable-sized and will typically be
	// smaller (e.g. 800, 80, or even 7 nonces). No rigid framing is applied.
	NoncePageSize int

	// DispatchMaxRetries is the maximum re-queue attempts for a failed page
	// before it is moved to the dead-letter list as a permanent failure.
	DispatchMaxRetries int

	// DispatchACKTimeout is how long a worker waits for the client's Ack
	// after sending one AccountSyncResponse page before treating it as failed.
	DispatchACKTimeout time.Duration
}

// DispatchPageMetrics captures per-page timings for observability.
// Emitted to DispatcherCallbacks.OnPageMetrics after every dispatch attempt.
// Success=false rows describe failed attempts; PageIndex=0 means the failure
// occurred before the page was ever taken off the nonce channel.
type DispatchPageMetrics struct {
	PageIndex  uint32
	NonceCount int
	DBFetchMS  int64
	SendMS     int64
	Retries    int
	Success    bool
}

// DeadLetterPage describes one page that exhausted all retries or could not
// be re-queued. FailureHint carries the last error message for diagnostics.
type DeadLetterPage struct {
	Nonces      []uint64
	RetryCount  int
	FailureHint string
}

// DispatchSummary is the end-of-session report returned by AccountDispatcher.Run.
// DeliveredPages + PermanentFailedPages = total pages admitted via EnqueueNonces.
type DispatchSummary struct {
	DeliveredPages          uint32
	DeliveredAccounts       uint64
	PermanentFailedPages    uint32
	PermanentFailedAccounts uint64
	DeadLetters             []DeadLetterPage
}

// DispatcherCallbacks decouples the dispatcher from DB and networking details.
// FetchAccounts and SendPage are required; the On* callbacks are optional.
//
// Using callbacks instead of direct dependencies keeps the dispatcher
// independently testable — pass lightweight mock functions in tests.
type DispatcherCallbacks struct {
	// FetchAccounts fetches full account records for the given nonces from the
	// DB. One call per page (~3000 nonces). The dispatcher sets balance="0"
	// before sending; the DB value is intentionally ignored.
	//
	// Accounts not found for a given nonce are silently omitted.
	FetchAccounts func(ctx context.Context, nonces []uint64) ([]*Account, error)

	// SendPage delivers one page of accounts to the client and waits for the
	// client's Ack. The caller handles proto conversion and network dialing.
	// pageIndex is the 1-based sequence number for this page in the session.
	SendPage func(ctx context.Context, pageIndex uint32, accounts []*Account) error

	// OnPageMetrics is called after every dispatch attempt (success or failure).
	// Optional — set to nil to skip metrics.
	OnPageMetrics func(ctx context.Context, m DispatchPageMetrics)

	// OnDeadLetter is called when a page exhausts all retries.
	// Optional — set to nil to skip.
	OnDeadLetter func(ctx context.Context, dead DeadLetterPage)
}
