# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

JMDN-FastSync is a **peer-to-peer block synchronization protocol** for the Jupiter Meta L2 blockchain, built on libp2p. It implements an eight-protocol approach: peer availability handshake, Merkle tree bisection to identify differing blocks, concurrent header fetching, full block data sync, point-of-time-sync (PoTS) to catch up on blocks produced during sync, account sync for zero-transaction accounts, Merkle sub-tree requests, and pub/sub block announcements.

**Language**: Go 1.25 | **Network**: libp2p (TCP + QUIC) | **Serialization**: Protocol Buffers | **External**: `JMDN_Merkletree` (ART + Merkle)

## Build & Test Commands

```bash
# Build the library
go build ./...

# Build interactive CLI
go build -o priorsync-cli ./cli.go

# Run all tests
go test ./...

# Run specific test package
go test ./core/protocol/router -v

# Run a single test
go test -run TestName ./path/to/package -v

# Regenerate proto (run from repo root)
protoc -I=common/proto --go_out=common/proto --go_opt=paths=source_relative \
  common/proto/<domain>/<file>.proto
```

There is no Makefile, linter config, or CI pipeline. Proto files are pre-compiled — generated `.pb.go` files are committed alongside the `.proto` sources.

---

## Sync Protocols

| # | Name | Protocol ID | Location | Purpose |
|---|------|-------------|----------|---------|
| 0 | Availability | `/fastsync/v1/availability` | `core/availability/` | Handshake: client gets UUID token, server reports block range |
| 1 | PriorSync | `/fastsync/v1/priorsync` | `core/priorsync/` | Merkle tree bisection to identify differing block ranges |
| 1a | Merkle | `/fastsync/v1/merkle` | `core/protocol/router/bisection.go` | On-demand sub-range Merkle requests during bisection |
| 2 | HeaderSync | `/fastsync/v1/headersync` | `core/headersync/` | Concurrent batch header fetching (1500/request) |
| 3 | DataSync | `/fastsync/v1/datasync` | `core/datasync/` | Full block data (txs, ZK proofs, L1 finality) |
| 4 | PoTS | `/fastsync/v1/pots` | `core/pots/` | Buffer + hydrate blocks produced during phases 1–3 |
| 5 | AccountSync | `/fastsync/v1/accountssync` | `core/protocol/router/data_router.go` | Sync accounts missing on client (zero-balance transfer) |
| — | PubSub | `/fastsync/v1/pubsub/blocks` | `core/pubsub/` | GossipSub-like block announcements to subscribers |

**SyncConfirmation** runs after HeaderSync: re-compares Merkle trees and loops until convergence (max 4 rounds).

**Reconciliation** runs after DataSync/AccountSync: replays transactions per account to compute final balances.

---

## Architecture: Protocol Flows

### Phase 0 — Availability
1. Client → Server: `AvailabilityRequest` (block range)
2. Server → Client: `AvailabilityResponse` (is_available, nodeinfo, UUID auth token)
3. Client stores UUID for all downstream phase auth

### Phase 1 — PriorSync + Merkle bisection
1. Client → Server: `PriorSyncMessage` (Merkle snapshot + checksum)
2. Server bisects MMR accumulator to find divergent block ranges
3. Server → Client: `PriorSyncMessage` (Tag of differing ranges + optional HeaderSync request)
4. Sub-range Merkle requests use a separate stream per sub-range query
5. Heartbeats every ~5s (server → client) during long Merkle computation

### Phase 2 — HeaderSync
1. Client batches Tag into requests (≤1500 headers each)
2. 3 concurrent workers fetch from remotes; failover to next remote on 2 retries
3. Headers written via `BlockInfo.NewHeadersWriter().WriteHeaders()`
4. SyncConfirmation: re-compare Merkle, loop until convergence (max 4 rounds)

### Phase 3 — DataSync
1. Client batches Tag into requests (≤30 blocks each)
2. 3 concurrent workers fetch `NonHeaders` (txs, ZK proofs, L1 finality)
3. Server response includes `TaggedAccounts` (accounts touched in those blocks)
4. Written via `BlockInfo.NewDataWriter().WriteData()`
5. Heartbeats every ~5s during heavy DB reads

### Phase 4 — PoTS
1. At T=0 (sync start), PoTS opens a dedicated WAL (`pots.wal`)
2. Blocks produced during phases 1–3 are buffered into PoTS WAL
3. After phases 1–3, client → server: `PoTSRequest` (blocks it has + timestamps)
4. Server → client: `PoTSResponse` (Tag of gaps)
5. Client fetches gaps via HeaderSync + DataSync again
6. PoTS WAL hydrated (flushed to main DB)

### Phase 5 — AccountSync (bidirectional streaming)
Syncs accounts that have zero transactions (not covered by DataSync TaggedAccounts).

**Client → Server upload (sequential, one batch at a time):**
1. Client builds ART of its account nonces (≤`MAX_ACCOUNT_NONCES` keys per chunk)
2. Each chunk zstd-encoded via `art.Encode()`, sent as `AccountNonceSyncRequest`
3. Server ACKs each batch with `AccountBatchAck` (client waits before sending next)
4. Final batch sets `is_last=true`

**Server diff computation:**
5. Server merges each ART chunk into `SwappableART` (auto-spills to disk at threshold)
6. Server sends `AccountSyncHeartbeat` every ~5s to keep stream alive during DB scan

**Server → Client response (parallel pages + sequential terminator):**
7. Server iterates all accounts via `BlockInfo.NewAccountNonceIterator()`
8. For each account nonce: if `!swappable.Contains(nonce)` → client is missing it
9. Missing accounts batched into pages, dispatched as parallel `AccountSyncResponse` goroutines
10. After all goroutines finish (`wg.Wait()`), server sends one `AccountSyncEndOfStream`
11. Client uses `total_pages` in EndOfStream to verify no pages were dropped

**Wire envelope**: All server→client frames wrapped in `AccountSyncServerMessage` (oneof).

**Safety**: `AccountSyncEndOfStream` is the only reliable completion signal — parallel response pages can arrive out of order so `has_more` in responses is not used.

### Reconciliation (post-DataSync + post-AccountSync)
For each account in TaggedAccounts, replays all its transactions to compute final balance:
1. `AccountManager.GetTransactionsForAccount()` — fetch all sender/receiver txs
2. Replay to compute final balance
3. `AccountManager.BatchUpdateAccounts()` — atomic batch write
4. LRU cache (200 entries) used for block header lookups

---

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `core/priorsync/` | Phase 1 client logic, `SetupNetworkHandlers()`, `PriorSync()` |
| `core/headersync/` | Phase 2 concurrent worker pool, batching, remote failover |
| `core/datasync/` | Phase 3 data fetching |
| `core/pots/` | Phase 4 PoTS state machine, WAL, request/response helpers |
| `core/availability/` | Peer capability handshake, FastsyncReady singleton |
| `core/pubsub/` | GossipSub publisher, subscriber stream management |
| `core/reconsillation/` | Account balance reconciliation, LRU header cache |
| `core/sync/` | Protocol handler registration (`sync_protocols.go`) |
| `core/protocol/router/` | `DataRouter` server dispatcher, Merkle bisection, auth helpers |
| `core/protocol/communication/` | Client-side libp2p stream sends, heartbeat-aware reads |
| `core/protocol/merkle/` | MMR accumulator building and comparison |
| `core/protocol/tagging/` | Block range tagging (Tag, RangeTag output of Merkle comparison) |
| `common/WAL/` | WAL: event adapters, write/replay/flush/truncate/hydration |
| `common/proto/` | 15 `.proto` files + generated `.pb.go` files |
| `common/types/` | `ZKBlock`, `Header`, `Account`, `Nodeinfo`, `BlockInfo`, constants |
| `common/messaging/` | Length-delimited protobuf framing, transport selection (TCP/QUIC) |
| `common/checksum/` | Two-version checksum for PriorSync payload integrity |
| `internal/pbstream/` | `WriteDelimited` / `ReadDelimited` for varint-framed protobufs |
| `internal/GRO/` | Internal service helpers |
| `logging/` | Async structured logging (Zap + Loki), ion fields builder |
| `tests/` | Router tests, LRU tests, checksum tests, interactive CLI example |

---

## Key Interfaces (`common/types/nodeinfo.go`)

### `BlockInfo` — main database integration point
```go
type BlockInfo interface {
    AUTH() AUTHHandler
    GetBlockNumber() uint64
    GetBlockDetails() PriorSync
    NewBlockIterator(start, end uint64, batchsize int) BlockIterator
    NewBlockHeaderIterator() BlockHeader
    NewBlockNonHeaderIterator() BlockNonHeader
    NewHeadersWriter() WriteHeaders
    NewDataWriter() WriteData
    NewAccountManager() AccountManager
    NewAccountNonceIterator(batchSize int) AccountNonceIterator  // AccountSync
}
```

### `AccountNonceIterator` — AccountSync diff computation
```go
type AccountNonceIterator interface {
    NextBatch() ([]*Account, error)   // nil slice + nil error = end of iteration
    TotalAccounts() (uint64, error)
    Close()
}
```

### `AccountManager` — reconciliation
```go
type AccountManager interface {
    GetTransactionsForAccount(address string) ([]DBTransaction, error)
    GetAccountBalance(address string) (*big.Int, uint64, error)
    UpdateAccountBalance(address string, balance *big.Int, nonce uint64) error
    CreateAccount(address string, balance *big.Int, nonce uint64) error
    BatchUpdateAccounts(updates []AccountUpdate) error
}
```

### `AUTHHandler` — UUID/TTL session management
```go
type AUTHHandler interface {
    AddRecord(PeerID, UUID) error
    RemoveRecord(PeerID) error
    GetRecord(PeerID) (AUTHStructure, error)
    IsAUTH(PeerID, UUID) (bool, error)
    ResetTTL(PeerID) error
}
```

### `Nodeinfo` struct
```go
type Nodeinfo struct {
    PeerID       peer.ID
    Multiaddr    []multiaddr.Multiaddr
    Capabilities []string
    PublicKey    []byte
    Version      uint16     // V1 = TCP only, V2+ = QUIC primary + TCP fallback
    Protocol     protocol.ID
    BlockInfo    BlockInfo
    ART          *art.ART   // SwappableART for AccountSync
}
```

---

## Important Patterns

### WAL-first writes
All sync data is written to the WAL before the database for crash recovery. Event adapters in `common/WAL/adapter.go`:
- `HeaderSyncEvent` — header batch writes
- `MerkleSyncEvent` — Merkle tree snapshots
- `PriorSyncEvent` — prior sync state
- `DataSyncEvent` — block data writes
- `PoTSEvent` — blocks buffered during sync (in separate `pots.wal`)

### Heartbeat keepalive
Long-running server computations send `*Heartbeat` frames every ~5s to prevent libp2p stream read deadline expiry on the client. Pattern in `sync_protocols.go`: launch heartbeat goroutine → run computation → close `done` channel → send final response. Cancel computation context if heartbeat write fails (peer gone).
- `StreamDeadline = 15s`, `HeartbeatInterval = 5s` (`ceil(15/3)`)
- Each protocol has its own heartbeat type: `priorsync.Heartbeat`, `DataSyncHeartbeat`, `PoTSHeartbeat`, `AccountSyncHeartbeat`

### Stream handler registration pattern (`sync_protocols.go`)
```
node.SetStreamHandler(protocolID, func(str network.Stream) {
    defer str.Close()
    // 1. Read delimited request
    // 2. Extract remoteNodeInfo from stream connection
    // 3. (Long ops only) Start heartbeat goroutine + computeCtx
    // 4. Route to Datarouter.HandleXxx(ctx, req, remoteNodeInfo)
    // 5. Stop heartbeats, write delimited response
})
```

### AccountSync stream pattern
AccountSync is the only bidirectional streaming protocol — others are single request/response with optional heartbeats. AccountSync stream lifecycle:
- Upload loop: read `AccountNonceSyncRequest` → merge into `SwappableART` → write `AccountBatchAck` → repeat until `is_last=true`
- Heartbeat goroutine fires during diff computation gap
- Response: parallel page dispatch → `wg.Wait()` → sequential `AccountSyncEndOfStream`
- All writes protected by a shared `sync.Mutex` (libp2p stream not goroutine-safe)

### Dependency injection via `SetSyncVars()`
Components are created then configured before use:
```go
component.SetSyncVars(ctx, version, nodeInfo, wal, host)
```

### Concurrent workers with failover
HeaderSync and DataSync use 3 concurrent workers. On fetch failure: retry same remote up to 2 times, then rotate to next available remote.

### SwappableART (`JMDN_Merkletree/art/swappable.go`)
Memory-bounded ART: hot in-memory + zstd segment files on disk.
- `NewSwappable(dir, threshold)` — threshold = keys before auto-spill
- `Merge(src *ART)` — bulk insert from another ART (triggers spill at threshold)
- `Contains(nonce)` — tombstone → hot ART → segment binary search (O(1) range filter + O(log n))
- `Close()` — flush remaining hot data
- `art.DefaultThreshold = 1_000_000`

### ART codec (`JMDN_Merkletree/art/codec.go`)
- `art.Encode(*ART) []byte` — delta-encode + zstd compress
- `art.Decode([]byte) (*ART, error)` — decompresses internally (no external decompression needed)

---

## Proto Files (`common/proto/`)

Generated `.pb.go` files live alongside their `.proto` sources. All 15 domains:

| File | Key Messages |
|------|-------------|
| `ack/ack.proto` | `Ack{ok, error}` |
| `phase/phase.proto` | `Phase{present_phase, successive_phase, success, error, auth}` |
| `nodeinfo/nodeinfo.proto` | `NodeInfo{peer_id, multiaddrs, capabilities, public_key, version}` |
| `availability/auth/auth.proto` | `Auth{UUID}` |
| `availability/availability.proto` | `AvailabilityRequest`, `AvailabilityResponse{is_available, nodeinfo, block_merge, auth, phase}` |
| `block/block.proto` | `Header`, `Transaction`, `ZKBlock` |
| `block/block_nonheader.proto` | `SnapshotRecord`, `ZKProof`, `DBTransaction`, `L1Finality`, `NonHeaders` |
| `merkle/merkle.proto` | `MerkleSnapshot`, `MerkleRequest`, `MerkleMessage`, `MerkleRequestMessage` |
| `tagging/tag.proto` | `Tag{block_number[], range[]}`, `RangeTag{start, end}`, `TaggedAccounts` |
| `priorsync/priorsync.proto` | `PriorSync`, `PriorSyncMessage`, `StreamMessage{oneof: heartbeat\|response}` |
| `headersync/headersync.proto` | `HeaderSyncRequest`, `HeaderSyncResponse{header[]}` |
| `datasync/datasync.proto` | `DataSyncRequest`, `DataSyncResponse{data[], taggedaccounts}`, `DataSyncStreamMessage` |
| `pots/pots.proto` | `PoTSRequest{blocks map, latest_block_number}`, `PoTSResponse`, `PoTSStreamMessage` |
| `accounts/accounts.proto` | `Account`, `AccountNonceSyncRequest`, `AccountBatchAck`, `AccountSyncHeartbeat`, `AccountSyncResponse`, `AccountSyncEndOfStream`, `AccountSyncServerMessage{oneof}` |
| `pubsub/pubsub.proto` | `SubscribeRequest`, `SubscribeResponse`, `BlockPubSubMessage{oneof: block\|done}` |

---

## Constants (`common/types/constants/`)

### `constants.go` — phase state names and limits
```go
// Phase state name strings (used in Phase.present_phase / successive_phase)
SYNC_REQUEST, SYNC_REQUEST_AUTOPROCEED, SYNC_REQUEST_RESPONSE, FULL_SYNC_REQUEST
HEADER_SYNC_REQUEST, HEADER_SYNC_RESPONSE
MERGE_REQUEST, MERGE_RESPONSE
REQUEST_MERKLE, RESPONSE_MERKLE
DATA_SYNC_REQUEST, DATA_SYNC_RESPONSE
AVAILABILITY_REQUEST, AVAILABILITY_RESPONSE
PoTS_REQUEST, PoTS_RESPONSE
ACCOUNTS_SYNC_REQUEST, ACCOUNTS_SYNC_RESPONSE
FAILURE, SUCCESS, UNKNOWN

// Limits
MAX_HEADERS_PER_REQUEST = 1500
MAX_DATA_PER_REQUEST    = 30
MIN_BLOCKS              = 500    // full sync if client has fewer blocks
MAX_PARALLEL_REQUESTS   = 10
ATMOST_ACCOUNT_ROUTINES = 15
LRU_CACHE_CAPACITY      = 200

// Stream timing
StreamDeadline    = 15 * time.Second
HeartbeatInterval = 5 * time.Second  // ceil(StreamDeadline / 3)
```

### `protocols.go` — libp2p protocol IDs
```go
AvailabilityProtocol  = "/fastsync/v1/availability"
PriorSyncProtocol     = "/fastsync/v1/priorsync"
MerkleProtocol        = "/fastsync/v1/merkle"
HeaderSyncProtocol    = "/fastsync/v1/headersync"
DataSyncProtocol      = "/fastsync/v1/datasync"
AccountsSyncProtocol  = "/fastsync/v1/accountssync"
PoTSProtocol          = "/fastsync/v1/pots"
BlocksPUBSUB          = "/fastsync/v1/pubsub/blocks"
```

### `accounts_constants.go` — AccountSync limits
```go
MAX_ACCOUNT_NONCES = 100_000   // max accounts per AccountSyncResponse page
SWAP_DISK_WINDOW   = 10 * 1024 // SwappableART disk spill window (10 MB)
```

---

## DataRouter (`core/protocol/router/data_router.go`)

Central server-side dispatcher. All `HandleXxx` methods authenticate via `router.Authenticate()` and defer `router.ResetTTL()`.

| Method | Purpose |
|--------|---------|
| `HandleAvailability(ctx, req, remote)` | Rate-limited (3 burst / 2/min per peer), availability response |
| `HandlePriorSync(ctx, req, remote)` | Route on `phase.PresentPhase`, call `SYNC_REQUEST_V2()` |
| `HandleMerkle(ctx, req, remote)` | Build sub-range Merkle tree, return snapshot |
| `HandleHeaderSync(ctx, req, remote)` | Fetch headers from BlockInfo, return batch |
| `HandleDataSync(ctx, req, remote)` | Fetch NonHeaders + compute TaggedAccounts |
| `HandlePoTSync(ctx, req, remote)` | Compute PoTS gap Tag from buffered blocks |
| `HandleAccountsSync(ctx, req, remote)` | AccountSync: currently routes to `ACCOUNTS_SYNC()` |

Internal computation methods: `SYNC_REQUEST_V2()`, `REQUEST_MERKLE()`, `HEADER_SYNC()`, `DATA_SYNC()`, `ACCOUNTS_SYNC()`.

Auth helpers (`auth_helper.go`): `Authenticate(ctx, auth, remote) (bool, error)`, `ResetTTL(ctx, auth, remote) error`.

---

## WAL System

**`common/types/wal/wal.go`** — types, constants, `EventAdapter` interface
**`common/WAL/WAL.go`** — WAL struct, `Append`, `Flush`, `Replay`, `Truncate`
**`common/WAL/adapter.go`** — concrete event types: `HeaderSyncEvent`, `MerkleSyncEvent`, `PriorSyncEvent`, `DataSyncEvent`
**`common/WAL/operations.go`** — batch write helpers
**`common/WAL/hydration/hydration.go`** — WAL replay → DB write on recovery

WAL types: `HeaderSync`, `MerkleSync`, `PriorSync`, `DataSync`, `Reconciliation`, `PoTS`
PoTS uses a separate isolated WAL file (`pots.wal`) to keep PoTS events separate from FastSync WAL.

---

## Messaging & Transport (`common/messaging/`)

- **`SendProtoDelimited`** — single request/response with transport selection
- **`SendProtoDelimitedWithHeartbeat`** — PriorSync: handles `StreamMessage` oneof, resets deadline on heartbeat
- **`SendDataSyncProtoDelimitedWithHeartbeat`** — DataSync: handles `DataSyncStreamMessage` oneof
- **`SendPoTSProtoDelimitedWithHeartbeat`** — PoTS: handles `PoTSStreamMessage` oneof
- Transport selection (`utils.go`): V1 = TCP only; V2+ = QUIC primary with TCP fallback

**`internal/pbstream/`** — `WriteDelimited(w, msg)` / `ReadDelimited(r, msg)` using uvarint length prefix.

---

## Errors (`common/types/errors/`)

- `commonerrors.go` — `MetadataRequired`, `ChecksumMismatch`, `BlockInfoNil`, `AuthRequired`, `AuthenticationFailed`, `RateLimitExceeded`, `FastsyncNotAvailable`
- `accountsync.go` — `AccountsSyncRequestNil`, `AccountsSyncArtNil`
- `priorsyncerrors.go` — PriorSync-specific errors
- `Autherrors.go` — auth-specific errors

---

## Tests (`tests/`)

- `tests/router/` — `TestDivideTags` (tag helper splitting)
- `tests/LRUCache/` — LRU cache correctness
- `tests/checksum/` — checksum computation/verification
- `tests/helper/` — batch operation helpers
- `tests/example/` — interactive two-node CLI demo (`node.go`, `sendmessage.go`, `startlistening.go`)
- `tests/example/wal_demo/` — WAL checkpoint/replay demo

---

## External Dependencies (`JMDN_Merkletree/`)

- `art/art.go` — Adaptive Radix Tree: `Insert`, `Contains`, `Delete`, `Merge`, `Iter`, `Keys`
- `art/swappable.go` — `SwappableART`: hot ART + disk segments, `DefaultThreshold = 1_000_000`
- `art/codec.go` — `Encode(*ART) []byte`, `Decode([]byte) (*ART, error)` (zstd + delta encoding)
- `merkletree/` — MMR accumulator builders and bisection helpers used by PriorSync

---

## AccountSync Implementation Status

| Component | Status |
|-----------|--------|
| `accounts.proto` + `accounts.pb.go` | Complete |
| `constants.ACCOUNTS_SYNC_REQUEST/RESPONSE` | Complete |
| `constants.AccountsSyncProtocol` | Complete |
| `constants.MAX_ACCOUNT_NONCES` | Complete (100,000) |
| `BlockInfo.NewAccountNonceIterator()` | Interface defined, needs DB implementation |
| `AccountNonceIterator` interface | Complete |
| `HandleAccountsSync` in `data_router.go` | Stub (routes to `ACCOUNTS_SYNC`) |
| `ACCOUNTS_SYNC` in `data_router.go` | Partial (case false: decode+merge done; case true: diff computation TODO) |
| `HandleAccountsSync` in `sync_protocols.go` | Not yet registered |
| Client-side `core/accountsync/` | Not yet created |
| WAL event `AccountSyncEvent` | Not yet added |

---

## Documentation

- `docs/WAL.md` — WAL implementation details and replay semantics
- `docs/POTS.md` — PoTS protocol specification
- `README.md` — Full architecture overview with diagrams and usage examples
