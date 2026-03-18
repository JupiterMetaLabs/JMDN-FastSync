# POTS — Point of Time Sync

## Overview

FastSync synchronises a node to a fixed point in history — it fetches all blocks up to
the moment sync began, then stops. But the chain does not stop. By the time FastSync
completes (PriorSync → HeaderSync → DataSync → Reconciliation), new blocks have been
produced and the node is already behind.

**POTS bridges this gap.** It runs as a concurrent goroutine from the very first moment
FastSync begins. While FastSync is catching up to the past, POTS is quietly recording
the present — so that when FastSync finishes, the node can replay everything it missed
and become fully current without requesting or re-downloading anything it already has.

---

## Problem POTS Solves

```
T=0                   T=sync_end         T=current
│                     │                  │
│◄──── FastSync ──────►│                  │
│                     │◄──── GAP ────────►│
│                     │  (blocks produced │
│                     │   during sync)    │
```

Without POTS, the node must start another round of sync to fill the gap — and by the
time that finishes, another gap has appeared. POTS eliminates this by continuously
buffering the gap into a local WAL while FastSync runs.

---

## Constraints

- The node **does not participate in consensus** while POTS is active.
- The node **does not vote** during the sync window.
- The node **does ingest finalised block results** — blocks that have already reached
  global consensus and are immutable. It never touches in-progress consensus rounds.
- No new peer connections are opened during FastSync. POTS opens a **fresh connection**
  using the same protocol stack after FastSync completes — clean file descriptor
  lifecycle, no stream contention.

---

## Peer Selection

POTS accepts a priority-ordered peer list as input. The caller decides the strategy:

| Strategy | Description |
|---|---|
| **Primary** | The peer that served DataSync — already trusted, most likely to have the exact block range |
| **Fallback** | Any peer from the seed node list |
| **Explicit** | Operator-specified peer ID |

POTS tries peers in order. If a peer fails mid-stream it falls back to the next. The
selection policy is owned by the caller, not by POTS itself.

---

## Phases

### Phase 1 — Tag Request

The Target sends the POTS request to a server peer. The request contains one field:
**the timestamp at which FastSync began** (`sync_start_time`).

The server maps this timestamp to the nearest block number in its DB, then responds
with a list of **block tags** from that block up to its current chain head — the same
lightweight tag format used in HeaderSync. No block bodies are sent at this stage.

```
Target ──[sync_start_time]──► Server
Target ◄──[block tags: B_n … B_latest]── Server
```

---

### Phase 2 — Local Deduplication

The Target receives the tag list and filters it against two local sources:

1. **Local POTS WAL** — blocks already buffered during the FastSync window
2. **Local DB** — blocks already committed (identified by the last known block number)

Any tag whose block number already exists in either source is discarded. The remainder
is the **missing set** — blocks the Target needs to request.

This step ensures the Target never re-downloads data it already holds, regardless of
whether that data arrived via the original FastSync or the POTS buffer.

---

### Phase 3 — Block Fetch

The Target sends the missing set of block tags back to the server. The server responds
with full block data for only those blocks — same response format as DataSync.

```
Target ──[missing tags]──► Server
Target ◄──[block bodies for missing tags]── Server
```

No unnecessary data is transferred. If the missing set is empty (the POTS WAL already
captured everything), this phase is skipped entirely.

---

### Phase 4 — WAL Hydration

Fetched blocks are written to the **POTS WAL** — not directly to the DB. The WAL is
the source of truth for this phase. Writing to WAL first gives crash safety: if the
process dies between fetch and DB commit, the blocks are not lost and hydration can
resume from the WAL on restart.

**Concurrent buffering during this phase:** new blocks finalised on the network during
Phases 1–4 are continuously appended to the same WAL by the background collector
goroutine. The WAL is a producer-consumer queue:

```
Writer (collector) ──► [WAL: B_n … B_latest … B_new1 … B_new2]
                                  ▲
                        Reader (hydration) consuming from front
```

The writer never stops until the goroutine receives an explicit shutdown signal.

---

### Phase 5 — DB Commit and Reconciliation

The hydration reader consumes blocks from the POTS WAL front and commits them to the
DB. Once blocks are committed, the Reconciliation protocol runs to update account
balances and nonces — the same reconciliation module used at the end of FastSync.

The reader tracks two cursor positions:

- **Write cursor** — where the collector last wrote
- **Read cursor** — where hydration last committed

When `read cursor == write cursor` and the server stream is closed (no new blocks are
being produced), the POTS WAL is fully drained and the node's DB reflects the current
chain state.

---

### Phase 6 — Live Gate

After WAL drain, the node does not immediately enter consensus. It waits for the
**currently active consensus round** to finish. In the following round it participates
as a live voting node.

```
WAL drained
     │
     ▼
Wait for active round to complete
     │
     ▼
Join next round as live participant
```

This prevents the node from injecting a vote mid-round with potentially stale state.

---

## Divergence Detection

During Phase 5, the WAL queue should shrink over time — hydration processes blocks
faster than the network produces them. If the queue grows instead (write cursor
advancing faster than read cursor), it indicates the DB write throughput cannot keep
pace with block production. This is exposed as a measurable signal so the operator can
observe it. It is a hardware or configuration problem, not a protocol failure.

---

## Full Lifecycle

```
T=0  FastSync starts
│    └── POTS goroutine starts → enters HOLD state (collector running, no hydration)
│
│    [FastSync: PriorSync → HeaderSync → DataSync → Reconciliation]
│    [POTS: continuously buffering new finalised blocks into POTS WAL]
│
T=reconciliation_complete
│    └── Signal sent to POTS goroutine → exits HOLD, begins Phase 1
│
│    Phase 1: Tag Request (send sync_start_time, receive block tags)
│    Phase 2: Local Deduplication (filter against WAL + DB)
│    Phase 3: Block Fetch (request only missing blocks)
│    Phase 4: WAL Hydration (write fetched blocks to POTS WAL)
│             [collector goroutine still appending new blocks concurrently]
│    Phase 5: DB Commit + Reconciliation (drain WAL → DB → reconcile accounts)
│             [collector goroutine still running]
│
T=WAL_drained
│    └── Collector goroutine receives shutdown signal → exits
│    └── Node DB is current with chain head
│
│    Wait for active consensus round to finish
│
T=next_round
     └── Node enters live consensus as full participant
```

---

## Integration with Existing Modules

| Existing module | Role in POTS |
|---|---|
| **WAL** | POTS WAL is a separate WAL instance — same implementation, isolated file path |
| **DataSync protocol** | Reused for Phase 3 block fetch — no new protocol needed |
| **HeaderSync tag format** | Reused for Phase 1 tag response — no new message type needed |
| **Reconciliation** | Runs at end of Phase 5 — same module, same inputs |
| **Auth / peer handshake** | Reused — POTS opens a fresh connection using the same credentials |

---

## What POTS Does Not Own

- **Peer discovery** — uses the seed node list already fetched during FastSync
- **Block validation** — trusts the same server trust model as DataSync
- **Consensus timing** — the live gate decision belongs to the node, not to POTS
- **Account state** — Reconciliation owns this; POTS only delivers the raw blocks
