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