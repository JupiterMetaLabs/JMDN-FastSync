package constants

const (
	MAX_ACCOUNT_NONCES = 100_000
	// Beyond this window, Radix Trie would be swapped to disk to avoid memory exhaustion.
	SWAP_DISK_WINDOW = 10 * 1024 // 10MB

	// MaxMissingAccountsInMemory is a hard safety cap on the number of accounts
	// that ComputeAccountDiff will hold in the in-memory Missing map at once.
	// If the diff exceeds this count the call returns an error rather than OOM-ing.
	//
	// This cap is a temporary guard. The permanent fix is to replace the Missing map
	// with a streaming channel so parent goroutines write directly to the wire;
	// that work should be done together with the AccountSync router integration (item 1).
	MaxMissingAccountsInMemory = 10_000_000 // ~10 M accounts ≈ ~1 GB worst-case
)

// ── Topology constants ─────────────────────────────────────────────────────────
//
// At peak, 30 parents × 10 000 records = 300 000 accounts live in memory at once.
// Each parent dispatches its 10 000-record window to 10 children (1 000 each).
const (
	NumDiffParents     = 30     // parallel parent goroutines (work consumers)
	NumDiffChildren    = 10     // child goroutines per parent batch
	ParentMemoryWindow = 10_000 // max accounts loaded per parent in one iteration
	ChildWindow        = 1_000  // accounts per child slice (parentMemoryWindow / numDiffChildren)

	// fullSyncThresholdPct: if the client holds fewer than this percentage of the
	// server's total accounts, skip the ART diff and return every server account.
	FullSyncThresholdPct = 6

	GroApp       = "app:accountssync:diff"
	GroLocal     = "local:accountssync:diff"
	ParentWGName = "account-diff-parent"
)

const (
	TEMP_ART_DIR = "/jmdn-accountsync-art"
)

// ── Concurrent dispatch constants ──────────────────────────────────────────────
//
// During AccountSync response, missing accounts are split into batches and
// dispatched concurrently to keep bandwidth utilization high while maintaining
// memory bounds. Each batch becomes one AccountSyncResponse page.
//
// At peak: MaxAccountsPerBatch × NumConcurrentBatches = 3000 × 10 = 30,000
// accounts dispatched per iteration.
const (
	MaxAccountsPerBatch  = 3_000 // accounts per AccountSyncResponse page
	NumConcurrentBatches = 10    // concurrent workers (parallel pages in flight)
)

// ── Async streaming dispatcher constants ───────────────────────────────────────
//
// The dispatcher is an async pipeline between the diff stage and the network
// dispatch stage. It uses a bounded nonce channel with soft pause/resume
// hysteresis to prevent the diff stage from outrunning the dispatch stage.
//
// Pause/resume threshold:
//   - Diff goroutines pause when nonces in channel ≥ NonceBufferPauseThreshold.
//   - They resume only when the count drops below:
//       NonceBufferPauseThreshold × NonceBufferResumePct / 100  =  80,000
//   - The 20k gap prevents oscillation at the boundary (hysteresis).
//
// Page sizing:
//   - NoncePageSize is the MAXIMUM nonces per page. The last page of a diff
//     session will typically be smaller (e.g. 800, 80, or even 7 nonces).
//     The dispatcher handles variable-sized pages naturally — no rigid framing.
//
// Channel capacity is computed at runtime as:
//   ceil(NonceBufferPauseThreshold / NoncePageSize)
// and is not a named constant so it stays in sync with the two values above
// without manual maintenance.
const (
	// NonceBufferPauseThreshold is the soft upper limit on nonces buffered in
	// the dispatcher. Diff goroutines pause when this count is reached and
	// resume only when the count drops below NonceBufferResumePct % of this.
	NonceBufferPauseThreshold = 100_000

	// NonceBufferResumePct is the percentage of NonceBufferPauseThreshold at
	// which paused diff goroutines are allowed to resume.
	// Resume threshold = NonceBufferPauseThreshold × NonceBufferResumePct / 100
	//                  = 80,000 nonces.
	NonceBufferResumePct = 80

	// NoncePageSize is the maximum number of nonces in one dispatcher page.
	// Each full page → one DB batch-fetch → one AccountSyncResponse to client.
	// The final page of a session is variable-sized and may be much smaller.
	NoncePageSize = 3_000

	// DispatchWorkers is the number of concurrent goroutines that drain the
	// nonce channel. Each worker: DB fetch (3000 nonces) → proto convert →
	// dial client → send page → wait ACK. 10 workers × 3000 = 30,000 accounts
	// in flight across all workers at peak.
	DispatchWorkers = 10

	// DispatchMaxRetries is the maximum re-queue attempts for a failed page
	// before it is moved to the dead-letter channel and logged as permanent failure.
	DispatchMaxRetries = 3

	// DispatchACKTimeout is how long a dispatch worker waits for the client's
	// Ack after sending one AccountSyncResponse page before treating it as failed.
	DispatchACKTimeout = 10 // seconds
)
