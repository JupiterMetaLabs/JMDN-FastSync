package types

import (
	"context"
	"io"
	"time"
)

// ─── AccountSync Dispatcher Types ─────────────────────────────────────────────

// AccountSyncStream is the minimal interface a persistent dispatch stream must
// satisfy. libp2p network.Stream satisfies this automatically — no adapter needed.
type AccountSyncStream interface {
	io.ReadWriter
	io.Closer
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

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
//
// FetchAccounts, OpenStream, and SendPageOnStream are required.
// The On* callbacks are optional (set to nil to skip).
//
// Change from single-stream-per-page to persistent-stream-per-worker:
//   - OpenStream is called once per worker at startup.
//   - SendPageOnStream writes one page to an already-open stream and reads the ACK.
type DispatcherCallbacks struct {
	// FetchAccounts fetches full account records for the given nonces.
	// Called once per page. Implementations should use a connection pool
	// to avoid creating a new DB session per call.
	FetchAccounts func(ctx context.Context, nonces []uint64) ([]*Account, error)

	// OpenStream opens a persistent stream to the client for this worker.
	// Called once per worker at startup (and once on reopen after a stream error).
	OpenStream func(ctx context.Context) (AccountSyncStream, error)

	// SendPageOnStream delivers one page on an already-open stream and waits
	// for the client's ACK. pageIndex is the 1-based sequence number.
	SendPageOnStream func(ctx context.Context, stream AccountSyncStream, pageIndex uint32, accounts []*Account) error

	// OnPageMetrics is called after every dispatch attempt. Optional.
	OnPageMetrics func(ctx context.Context, m DispatchPageMetrics)

	// OnDeadLetter is called when a page exhausts all retries. Optional.
	OnDeadLetter func(ctx context.Context, dead DeadLetterPage)
}
