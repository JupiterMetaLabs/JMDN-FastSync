# JMDN-FastSync

A peer-to-peer block synchronization protocol built on **libp2p** for the Jupiter Meta L2 blockchain. FastSync identifies missing or divergent blocks between two nodes using Merkle tree comparison, then synchronizes headers and data in distinct phases — each with integrity verification, WAL-based crash recovery, and heartbeat keepalive for long-running computations.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Sync Phases](#sync-phases)
  - [Phase 1 — PriorSync](#phase-1--priorsync)
  - [Phase 2 — HeaderSync](#phase-2--headersync)
  - [Phase 3 — DataSync](#phase-3--datasync)
  - [Phase 4 — SyncConfirmation](#phase-4--syncconfirmation)
- [Protocols](#protocols)
- [Key Components](#key-components)
  - [Communication Layer](#communication-layer)
  - [Data Router](#data-router)
  - [Merkle Tree Comparison](#merkle-tree-comparison)
  - [Write-Ahead Log (WAL)](#write-ahead-log-wal)
  - [Checksum Verification](#checksum-verification)
- [Types & Interfaces](#types--interfaces)
- [Usage](#usage)
  - [Setting Up a Node](#setting-up-a-node)
  - [Running PriorSync (Client)](#running-priorsync-client)
  - [Running HeaderSync (Client)](#running-headersync-client)
  - [Full Simulation Example](#full-simulation-example)
- [Project Structure](#project-structure)

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Node 2 (Requester)                            │
│                                                                      │
│   PriorSync.PriorSync(remote)                                        │
│     ├─ GetBlockDetails()        ─── local chain state                │
│     ├─ GenerateMerkleTree()     ─── build local Merkle snapshot      │
│     ├─ Checksum.Create()        ─── integrity hash                   │
│     ├─ comm.SendPriorSync() ────── libp2p stream ─────────┐         │
│     ├─ WAL.WriteEvent()         ─── persist response       │         │
│     └─ return proto response                               │         │
│                                                             ▼         │
│   ┌─────────────────────────────────────────────────────────────┐    │
│   │                    Node 1 (Responder)                       │    │
│   │                                                             │    │
│   │  SetupNetworkHandlers()                                     │    │
│   │    ├─ HandlePriorSync  ─ heartbeat keepalive ─┐             │    │
│   │    │   └─ DataRouter.HandlePriorSync()        │             │    │
│   │    │       ├─ Verify checksum                 │             │    │
│   │    │       ├─ Reconstruct remote Merkle tree  │             │    │
│   │    │       ├─ Build local Merkle tree         │  heartbeats │    │
│   │    │       ├─ Compare trees (bisection)       │  every 5s   │    │
│   │    │       ├─ Tag differing ranges            │             │    │
│   │    │       └─ Return HeaderSyncRequest + Ack  ◄─────────────┘    │
│   │    ├─ HandleMerkle     ─ sub-tree requests                  │    │
│   │    ├─ HandleHeaderSync ─ serve block headers                │    │
│   │    └─ HandleDataSync   ─ serve block data                   │    │
│   └─────────────────────────────────────────────────────────────┘    │
│                                                                      │
│   HeaderSync.HeaderSync(tag, remotes)                                │
│     ├─ Build batches from tagged ranges                              │
│     ├─ Fetch headers concurrently (workers)                          │
│     ├─ WAL.WriteEvent() per batch                                    │
│     ├─ WriteHeaders() to DB                                          │
│     └─ SyncConfirmation() ─── re-compare Merkle trees                │
└──────────────────────────────────────────────────────────────────────┘
```

---

## Sync Phases

### Phase 1 — PriorSync

**Purpose**: Discover what blocks the remote node has that we don't (and vice versa).

**State constants**: `SYNC_REQUEST` → `SYNC_REQUEST_RESPONSE`

**How it works**:

1. The **requester** (Node 2) builds a Merkle tree snapshot over its local blocks `[0, blockNumber]` and sends it along with block metadata (block number, state root, block hash) and a checksum to the responder.
2. The **responder** (Node 1) receives the request, verifies the checksum, and builds its own Merkle tree for the same range.
3. The two Merkle trees are compared using **bisection** — recursively narrowing down sub-ranges to find exactly which block ranges differ.
4. The differing ranges are encoded as a **Tag** (a list of `(start, end)` ranges and individual block numbers) and returned in a `HeaderSyncRequest`.
5. The response is written to the **WAL** for crash recovery.

**Key features**:

- **Heartbeat keepalive**: The responder sends heartbeat messages every ~5 seconds during the (potentially long) Merkle tree computation to prevent stream timeouts.
- **Checksum verification**: The request payload is integrity-checked before processing.
- **Auto-proceed mode** (`SYNC_REQUEST_AUTOPROCEED`): Skips returning to the client and immediately proceeds to HeaderSync.

```go
// Requester side
resp, err := prior.PriorSync(0, localBlockNum, 0, math.MaxUint64, remoteNodeinfo)
```

---

### Phase 2 — HeaderSync

**Purpose**: Fetch the block headers that were identified as missing/different in Phase 1.

**State constants**: `HEADER_SYNC_REQUEST` → `HEADER_SYNC_RESPONSE`

**How it works**:

1. The Tag from Phase 1 is split into fixed-size batches (`MAX_HEADERS_PER_REQUEST = 1500`).
2. Batches are fetched **concurrently** from multiple remotes using a worker pool.
3. Each batch is written to the **WAL** before being written to the local database — ensuring crash recoverability.
4. Results are sorted by block number and written sequentially to maintain ordering.
5. If a worker fails to fetch from one remote, it retries with the **next available remote** (failover).

**Key features**:

- **Concurrent batched fetching** with configurable worker count
- **WAL write-ahead** before every DB write
- **Remote failover** on fetch failures
- **Retry with re-enqueueing** when sync confirmation shows remaining diffs

```go
// After PriorSync returns a tagged response
if resp.Headersync != nil && resp.Headersync.Tag != nil {
    err = header.HeaderSync(resp.Headersync, remotes)
}
```

---

### Phase 3 — DataSync

**Purpose**: Fetch the full block data (transactions, ZK proofs, snapshots, L1 finality records) for synchronized blocks.

**State constants**: `DATA_SYNC_REQUEST` → `DATA_SYNC_RESPONSE`

**Proto payload** (`NonHeaders`):
| Field | Description |
|-------|-------------|
| `SnapshotRecord` | Block hash + creation timestamp |
| `DBTransaction[]` | Transactions with index and timestamp |
| `ZKProof` | STARK proof, commitment, and proof hash |
| `L1Finality` | L1 settlement confirmation + block numbers |

> **Status**: DataSync handler is registered but the full implementation is in progress.

---

### Phase 4 — SyncConfirmation

**Purpose**: Verify that HeaderSync has converged — both nodes should now have identical Merkle trees.

**How it works**:

1. After HeaderSync writes all headers, `SyncConfirmation` rebuilds the local Merkle tree.
2. Sends a new PriorSync request to a remote to compare trees.
3. If trees match → `DATA_SYNC_REQUEST` phase (headers in sync, proceed to data sync).
4. If trees still differ → returns remaining Tag for another round of HeaderSync.
5. Repeats until convergence or max rounds exceeded.

```go
// Called automatically inside HeaderSync
tag, inSync, err := header.SyncConfirmation(ctx, remotes)
if inSync {
    // Headers match — proceed to data sync
}
```

---

## Protocols

All protocols are registered as libp2p stream handlers under the `/priorsync/v1` namespace:

| Protocol ID                | Purpose                              | Handler            |
| -------------------------- | ------------------------------------ | ------------------ |
| `/priorsync/v1`            | PriorSync request/response           | `HandlePriorSync`  |
| `/priorsync/v1/merkle`     | Merkle sub-tree requests (bisection) | `HandleMerkle`     |
| `/priorsync/v1/headersync` | Header batch requests                | `HandleHeaderSync` |
| `/priorsync/v1/datasync`   | Full block data requests             | `HandleDataSync`   |

---

## Key Components

### Communication Layer

**Package**: `core/protocol/communication`

Provides the client-side send methods over libp2p streams:

| Method                    | Description                                  |
| ------------------------- | -------------------------------------------- |
| `SendPriorSync()`         | Heartbeat-aware PriorSync request            |
| `SendAutoSyncRequest()`   | Auto-proceed PriorSync (skips client return) |
| `SendMerkleRequest()`     | Request a Merkle sub-tree for a range        |
| `SendHeaderSyncRequest()` | Fetch headers for a specific block range     |

All methods use **length-delimited protobuf** framing via the `messaging` package. `SendPriorSync` specifically uses a heartbeat-aware read loop that handles interleaved `Heartbeat` and `Response` messages.

### Data Router

**Package**: `core/protocol/router`

The server-side request handler that dispatches to the correct logic:

| Method            | Phase      | Logic                                                   |
| ----------------- | ---------- | ------------------------------------------------------- |
| `SYNC_REQUEST`    | PriorSync  | Merkle comparison + bisection + tagging                 |
| `SYNC_REQUEST_V2` | PriorSync  | Simplified comparison (no bisection)                    |
| `REQUEST_MERKLE`  | Merkle     | Build and return sub-tree snapshot                      |
| `HeaderSync`      | HeaderSync | Read headers from DB, return to requester               |
| `FullSync`        | FullSync   | Full block range sync for small chains (`< 500` blocks) |

### Merkle Tree Comparison

**Package**: `core/protocol/merkle`

Uses an **MMR (Merkle Mountain Range)** accumulator with configurable chunk sizes. During PriorSync, the responder:

1. Reconstructs the requester's Merkle tree from the snapshot.
2. Builds its own tree for the same range.
3. Compares at each level — if roots differ, recursively bisects into sub-ranges.
4. For each sub-range, requests the remote's sub-tree via the Merkle protocol.
5. Tags all differing ranges for HeaderSync.

### Write-Ahead Log (WAL)

**Package**: `common/WAL`

Event-sourced WAL backed by [`tidwall/wal`](https://github.com/tidwall/wal) for crash recovery:

| Operation                     | Where Used                              |
| ----------------------------- | --------------------------------------- |
| `WriteEvent(HeaderSyncEvent)` | Before writing each header batch to DB  |
| `WriteEvent(PriorSyncEvent)`  | After receiving a PriorSync response    |
| `Flush()`                     | Ensures events are persisted to disk    |
| `ReplayEvents()`              | Recovers uncommitted events after crash |

Events are typed via the `EventAdapter` interface (`HeaderSyncEvent`, `PriorSyncEvent`, `MerkleSyncEvent`).

### Checksum Verification

**Package**: `common/checksum/checksum_priorsync`

Two-version checksum system that serializes the PriorSync payload (block number, state root, block hash, range) to protobuf bytes, then hashes them. Used to detect tampered or corrupted requests.

---

## Types & Interfaces

### `Nodeinfo`

```go
type Nodeinfo struct {
    PeerID       peer.ID
    Multiaddr    []multiaddr.Multiaddr
    Version      uint16            // V1=TCP, V2=TCP+QUIC
    BlockInfo    BlockInfo         // DB access interface
}
```

### `BlockInfo` Interface

The storage abstraction that any database must implement:

```go
type BlockInfo interface {
    GetBlockNumber() uint64
    GetBlockDetails() PriorSync
    NewBlockIterator(start, end uint64, batchsize int) BlockIterator
    NewBlockHeaderIterator() BlockHeader
    NewHeadersWriter() WriteHeaders
    NewDataWriter() WriteData
}
```

### `Syncvars`

Shared state passed to all sync components:

```go
type Syncvars struct {
    Ctx      context.Context
    Version  uint16
    NodeInfo Nodeinfo
    WAL      *WAL.WAL     // optional — nil skips WAL writes
}
```

---

## Usage

### Setting Up a Node

```go
import (
    "github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
    wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
    "github.com/JupiterMetaLabs/JMDN-FastSync/core/priorsync"
    "github.com/JupiterMetaLabs/JMDN-FastSync/core/headersync"
)

// 1. Create WAL instance (optional but recommended)
wal, err := WAL.NewWAL(wal_types.DefaultDir, 1)

// 2. Create routers
prior := priorsync.NewPriorSyncRouter()
header := headersync.NewHeaderSync()

// 3. Configure with node info, host and WAL
prior.SetSyncVars(ctx, protocolVersion, nodeinfo, host, wal)
header.SetSyncVars(ctx, protocolVersion, nodeinfo, host, wal)

// 4. Start listening for incoming sync requests (server side)
go func() {
    if err := prior.SetupNetworkHandlers(true); err != nil {
        log.Fatal(err)
    }
}()
```

### Running PriorSync (Client)

```go
// Send PriorSync to discover differences
// Args: localStart, localEnd, remoteStart, remoteEnd, remote
resp, err := prior.PriorSync(0, localBlockNum, 0, math.MaxUint64, remoteNodeinfo)
if err != nil {
    log.Fatal(err)
}
// resp is *priorsyncpb.PriorSyncMessage
// resp.Headersync.Tag contains the differing ranges (if any)
```

### Running HeaderSync (Client)

```go
// If PriorSync found differences, fetch the missing headers
if resp.Headersync != nil && resp.Headersync.Tag != nil {
    remotes := []*types.Nodeinfo{remoteNodeinfo}
    err = header.HeaderSync(resp.Headersync, remotes)
    if err != nil {
        log.Fatal(err)
    }
    // Headers are now written to local DB
    // SyncConfirmation runs automatically to verify convergence
}
```

### Full Simulation Example

See [`JMDN-Fastsync-Testsuite/Sync/sync.go`](https://github.com/JupiterMetaLabs/JMDN-Fastsync-Testsuite) for a complete two-node simulation that:

1. Creates two nodes with separate databases
2. Connects them via libp2p
3. Runs PriorSync to discover block differences
4. Runs HeaderSync to synchronize missing headers
5. Confirms sync convergence via Merkle tree comparison

---

