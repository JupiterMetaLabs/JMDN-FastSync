# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

JMDN-FastSync is a **peer-to-peer block synchronization protocol** for the Jupiter Meta L2 blockchain, built on libp2p. It uses a four-phase approach: Merkle tree comparison to identify differing blocks, concurrent header fetching, full block data sync, and point-of-time-sync (PoTS) to catch up on blocks produced during sync.

**Language**: Go 1.25 | **Network**: libp2p (TCP + QUIC) | **Serialization**: Protocol Buffers

## Build & Test Commands

```bash
# Build the library
go build ./...

# Build interactive CLI
go build -o priorsync-cli ./cli.go

# Run all tests
go test ./...

# Run specific test package
go test ./tests/router -v

# Run a single test
go test -run TestName ./path/to/package -v
```

There is no Makefile, linter config, or CI pipeline in this repo. Proto files are pre-compiled (generated `.pb.go` files are committed).

## Architecture: Four-Phase Sync

| Phase | Name | Protocol ID | Purpose |
|-------|------|-------------|---------|
| 1 | PriorSync | `/priorsync/v1` | Merkle tree comparison via bisection to identify differing block ranges |
| 2 | HeaderSync | `/priorsync/v1/headersync` | Concurrent fetching of block headers for tagged ranges |
| 3 | DataSync | `/priorsync/v1/datasync` | Fetch full block data (transactions, ZK proofs, L1 finality) |
| 4 | PoTS | `/priorsync/v1/pots` | Buffer blocks produced during phases 1-3, hydrate at end |

Additional protocols: `/priorsync/v1/merkle` (sub-tree requests during bisection), pubsub (block announcements via GossipSub).

**SyncConfirmation** runs after HeaderSync to re-compare Merkle trees and loop until convergence.

## Key Directories

- **`core/priorsync/`** — Phase 1 client logic and network handler setup (`SetupNetworkHandlers`, `PriorSync()`)
- **`core/headersync/`** — Phase 2 with concurrent worker pool, batching (1500 headers/request), remote failover
- **`core/datasync/`** — Phase 3 (in progress)
- **`core/pots/`** — Phase 4 PoTS state machine, WAL integration, request/response helpers
- **`core/protocol/router/`** — Server-side request dispatcher (`DataRouter`), Merkle bisection algorithm
- **`core/protocol/communication/`** — Client-side libp2p stream sends with heartbeat-aware reads
- **`core/protocol/merkle/`** — Merkle tree building and comparison (MMR accumulator)
- **`core/protocol/tagging/`** — Block range tagging (output of Merkle comparison)
- **`core/pubsub/`** — Pub/sub block announcements
- **`core/reconsillation/`** — Account balance reconciliation with LRU cache
- **`common/WAL/`** — Write-Ahead Log: event adapters, write/replay/flush/truncate operations
- **`common/proto/`** — 14 `.proto` files organized by domain (priorsync, headersync, merkle, tagging, block, etc.)
- **`common/types/`** — Core Go types: `ZKBlock`, `Header`, `Nodeinfo`, `BlockInfo` interface, constants
- **`common/messaging/`** — Length-delimited protobuf framing over libp2p streams
- **`common/checksum/`** — Two-version checksum for PriorSync payload integrity
- **`logging/`** — Async structured logging (Zap + Loki), builder pattern
- **`tests/example/`** — Interactive CLI demo for two-node sync

## Key Interfaces

**`BlockInfo`** (`common/types/nodeinfo.go`) — Storage abstraction that any database must implement. Methods include `GetBlockNumber()`, `NewBlockIterator()`, `NewBlockHeaderIterator()`, `NewHeadersWriter()`, `NewDataWriter()`, `NewAccountManager()`. This is the main integration point for connecting FastSync to a database.

**`Syncvars`** — Shared state (context, version, node info, WAL, host) passed to all sync components via `SetSyncVars()`.

## Important Patterns

- **WAL-first writes**: All sync data is written to the WAL before the database for crash recovery. Event types: `HeaderSyncEvent`, `PriorSyncEvent`, `MerkleSyncEvent`, `DataSyncEvent`.
- **Heartbeat keepalive**: PriorSync sends heartbeats every ~5s during long Merkle computations to prevent libp2p stream timeouts. Messages are multiplexed via `priorsync.StreamMessage` oneof.
- **Dependency injection**: Components are created then configured via `SetSyncVars()` before use.
- **Concurrent workers with failover**: HeaderSync uses a worker pool; on fetch failure, workers retry with the next available remote.

## Proto Files

Located in `common/proto/`. Generated Go code lives alongside the `.proto` files. Key message types:
- `priorsync.PriorSync` — Phase 1 request with Merkle snapshot + checksum
- `headersync.HeaderSyncRequest/Response` — Phase 2 with Tag (block ranges)
- `tagging.Tag` — Encoded differing block ranges (individual numbers + range pairs)
- `merkle.MerkleSnapshot` — Compact MMR representation
- `block.Header`, `block.NonHeaders`, `block.ZKBlock` — Block data structures

## Documentation

- `docs/WAL.md` — WAL implementation details
- `docs/POTS.md` — PoTS protocol specification
- `README.md` — Full architecture overview with diagrams and usage examples
