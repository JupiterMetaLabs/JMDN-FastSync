# AccountSync Performance Branch — Change Review

**Branch**: `fix/accountsync/performance`  
**Base**: `main` @ `a630fd8`  
**Commits ahead**:
- `480040b` — types: add AccountSyncStream interface + DispatcherCallbacks refactor  
- `00fd0eb` — WAL flushing error handling and logging  

---

## What problem does this branch solve?

The original AccountSync dispatch had two hot-path costs that compounded at scale:

1. **A new network stream was opened per page.** With 10 workers each handling hundreds of pages, that was hundreds of TCP/QUIC handshakes per sync session.
2. **A new `AccountManager` (DB session) was created per `FetchAccounts` call.** With 3,000 nonces per page and 10 workers running in parallel, that was continuous DB session churn.
3. **Each page was written to DB individually as it arrived on the client.** No batching — one DB transaction per page.

---

## Change 1 — `common/types/dispatcher.go`

### What changed
Added a new interface and rewired `DispatcherCallbacks`.

```go
// NEW — AccountSyncStream interface
type AccountSyncStream interface {
    io.ReadWriter
    io.Closer
    SetReadDeadline(t time.Time) error
    SetWriteDeadline(t time.Time) error
}
```

`DispatcherCallbacks` struct — before vs after:

| Field | Before | After |
|-------|--------|-------|
| (removed) | `SendPage func(ctx, pageIndex, accounts) error` | — |
| (added) | — | `OpenStream func(ctx) (AccountSyncStream, error)` |
| (added) | — | `SendPageOnStream func(ctx, stream, pageIndex, accounts) error` |
| unchanged | `FetchAccounts` | `FetchAccounts` |
| unchanged | `OnPageMetrics` | `OnPageMetrics` |
| unchanged | `OnDeadLetter` | `OnDeadLetter` |

### Why
`SendPage` was a single callback that both opened a stream AND sent data. Splitting it into `OpenStream + SendPageOnStream` lets a worker open a stream once at startup and reuse it across all its pages.

### Potential issues to look for
- `libp2p network.Stream` satisfies `AccountSyncStream` automatically (it implements all 4 methods) — but any mock or test double must now implement `SetReadDeadline` + `SetWriteDeadline` + `Close`, not just `Read`/`Write`.
- `OnDeadLetter` uses `OpenStream` internally to open a fresh one-off stream for recovery. If the client peer is already gone at dead-letter time, `OpenStream` will fail and the recovery attempt is silently abandoned (logged as error). **Is that the desired behaviour, or should it retry?**

---

## Change 2 — `core/protocol/router/helper/accounts/DispatcherCallbacks.go`

### What changed

#### 2a — `buildFetchAccounts` → `buildFetchAccountsWithPool`

Before:
```go
// NEW AccountManager created on EVERY FetchAccounts call
func buildFetchAccounts(nodeinfo *types.Nodeinfo) func(...) {
    return func(ctx context.Context, nonces []uint64) ([]*types.Account, error) {
        iter := nodeinfo.BlockInfo.NewAccountManager().NewAccountNonceIterator(1)
        defer iter.Close()
        return iter.GetAccountsByNonces(nonces)
    }
}
```

After:
```go
// Pool of numWorkers AccountManagers created ONCE at session start
func buildFetchAccountsWithPool(nodeinfo *types.Nodeinfo, numWorkers int) func(...) {
    pool := make(chan types.AccountManager, numWorkers)
    for i := 0; i < numWorkers; i++ {
        pool <- nodeinfo.BlockInfo.NewAccountManager()
    }
    return func(ctx context.Context, nonces []uint64) ([]*types.Account, error) {
        var mgr types.AccountManager
        select {
        case mgr = <-pool:
        case <-ctx.Done():
            return nil, ctx.Err()
        }
        defer func() { pool <- mgr }()

        iter := mgr.NewAccountNonceIterator(len(nonces))
        defer iter.Close()
        return iter.GetAccountsByNonces(nonces)
    }
}
```

**Potential issues:**
- Pool size = `numWorkers` (= `DispatchWorkers = 10`). Pool is a channel, so if all 10 workers hit `FetchAccounts` simultaneously, they each get their own manager with no contention — correct.
- `AccountManager` from jmdn (`immudb_account_manager`) is NOT a thread-safe struct. Two goroutines never share one manager since pool enforces 1-at-a-time borrow — correct.
- Pool is **never closed**. Managers are created at `buildFetchAccountsWithPool` call time and live for the session. If `AccountManager` holds a persistent DB connection, those connections remain open for the full AccountSync session. **Check: does jmdn's `NewAccountManager()` open a connection immediately, or on first use?**
- `batchSize` passed to `NewAccountNonceIterator` changed: was hardcoded `1`, now `len(nonces)`. **Check: does jmdn's `immudbNonceIter` use `batchSize` for anything other than cursor-based pagination? If it pre-allocates memory based on batchSize, passing 3000 is fine; if it has any upper bound check, verify 3000 doesn't hit it.**

#### 2b — `buildSendPage` → `buildOpenStream` + `buildSendPageOnStream`

Before: one callback that dialled the client, sent the page, and read the ACK — all in one call.

After: two callbacks:
- `buildOpenStream` — thin closure: calls `openFn(ctx, remote)`, returns stream.
- `buildSendPageOnStream` — builds the proto message, calls `sendFn(ctx, stream, msg)`, checks ACK.

Removed from `buildSendPageOnStream` log line:
```go
// REMOVED:
ion.String("from_peer", remote.PeerID.String())
```

**Potential issue:** The peer ID is no longer logged on the success ACK log line. May make debugging harder when tracing multi-peer sessions. Consider re-adding it or attaching it to context.

#### 2c — `buildOnDeadLetter` refactored

Before: used `buildFetchAccounts(nodeinfo)` (which creates a new AccountManager inline) + called `streamAccounts` directly.

After: uses `buildOpenStream`/`buildSendPageOnStream` internally — opens a one-off recovery stream and calls the same send path as normal pages.

```go
stream, err := open(ctx)      // one-off stream for dead-letter recovery
if err != nil { ... return }  // silently abandoned if stream open fails
defer stream.Close()

if err := send(ctx, stream, 0, accounts); err != nil { ... return }
```

**Potential issues:**
- Page index `0` is used for dead-letter recovery pages. Client side must handle `pageIndex=0` as a special recovery page, not a normal sequence number. **Verify the client-side handler (`HandleAccountsSyncData`) doesn't skip or miscount index 0.**
- ACK check was removed from dead-letter path. Before, the code explicitly checked `ack.GetBatchAck().GetAck().GetOk()`. Now `buildSendPageOnStream` handles ACK internally but `send(ctx, stream, 0, accounts)` only returns an `error` — the ACK check is inside `buildSendPageOnStream`. Confirm ACK rejection is still surfaced as an error.

---

## Change 3 — `core/protocol/router/helper/accounts/dispatcher/run.go`

### What changed

#### `dispatchWorker` — opens one stream at startup

Before: no stream; called `callbacks.SendPage(ctx, pageIndex, accounts)` which opened/closed a stream internally per page.

After:
```go
func (d *AccountDispatcher) dispatchWorker(ctx context.Context) {
    stream, err := d.callbacks.OpenStream(ctx)
    if err != nil {
        return   // ← worker exits silently if stream open fails
    }
    defer stream.Close()

    for {
        select {
        case <-d.done: return
        case <-ctx.Done(): return
        case page := <-d.nonceChan:
            streamOK := d.processPage(ctx, stream, page)
            if !streamOK {
                stream.Close()
                stream, err = d.callbacks.OpenStream(ctx)
                if err != nil {
                    return   // ← worker exits silently if reopen fails
                }
            }
        }
    }
}
```

**Potential issues:**
- If `OpenStream` fails at startup, the worker exits **silently** with no error recorded, no dead-letter, no inflight decrement. Pages assigned to this worker remain in `nonceChan` and will be drained by other workers — but if ALL workers fail to open streams, nonces sit in the channel until `ctx` times out. **Should a failed worker fire an error signal or at minimum log the failure?** Currently only the outer `Run()` ctx timeout will surface this.
- On stream reopen after a send failure: the failed page was already sent to `handleFailure` for re-queue/dead-letter. The stream is reopened on the NEXT iteration. This is correct — the page lifecycle is handled before the reopen.
- `stream.Close()` is called before reopen. If `Close()` errors (e.g. already half-closed by remote), that error is silently dropped. Fine for libp2p streams but worth noting.

#### `processPage` — now returns `streamHealthy bool`

```go
// DB fetch failure → return true  (stream is fine)
// Send failure    → return false  (stream may be broken)
// Success         → return true
```

**Potential issue:** A send failure causes `handleFailure` to re-queue the page (if retries remain) AND causes the worker to close/reopen the stream. On reopen, the re-queued page may be picked up by the SAME worker on the next iteration or by a DIFFERENT worker. Both are fine — pages are stateless. But verify that `handleFailure` re-queue path correctly restores `nonceBufferCount` before the stream reopen happens (it does — `handleFailure` runs before `processPage` returns `false`).

---

## Change 4 — `core/protocol/communication/communication.go`

### What changed
Two new methods added to the `Communicator` interface and the `communication` struct:

```go
// NEW on Communicator interface
OpenAccountsDataStream(ctx context.Context, peerNode types.Nodeinfo) (types.AccountSyncStream, error)
SendAccountPageOnStream(ctx context.Context, stream types.AccountSyncStream, msg *accountspb.AccountSyncServerMessage) (*accountspb.AccountSyncServerMessage, error)
```

`OpenAccountsDataStream` — dials the client peer on `AccountsSyncDataProtocol`, returns the raw stream without closing it. Caller owns lifetime.

`SendAccountPageOnStream` — delegates to `messaging.WriteAccountPageAndReadACK(stream, msg, constants.DispatchACKTimeout)`.

**Potential issues:**
- Any existing mock/stub of `Communicator` interface (in tests) must now implement these two methods or it won't compile. **Check all test files that mock `Communicator`.**
- `DispatchACKTimeout = 10s` is hardcoded here. If a page has 3,000 accounts, proto serialisation + network round-trip must fit in 10s. **Is 10s sufficient for the expected account payload size on mainnet? `3000 × ~200 bytes ≈ 600KB` per page — should be fine on LAN/QUIC but verify on real network conditions.**

---

## Change 5 — `common/messaging/messaging.go`

### What changed
Two new functions added:

```go
// Opens a persistent stream — caller owns Close()
func OpenAccountsSyncDataStream(ctx, version, host, peerInfo) (network.Stream, error)

// Write one page + read ACK on an already-open stream
func WriteAccountPageAndReadACK(stream streamReadWriter, msg, deadline) (*AccountSyncServerMessage, error)
```

`WriteAccountPageAndReadACK` uses a local `streamReadWriter` interface (unexported) that `network.Stream` satisfies automatically.

**Potential issues:**
- `OpenAccountsSyncDataStream` applies `constants.StreamDeadline` (15s) to the connect phase, but no deadline is set on the stream itself after open. Deadlines are applied per-write and per-read inside `WriteAccountPageAndReadACK`. This is intentional — the stream is reused across many pages. **Confirm there is no scenario where the stream sits idle long enough to trigger a libp2p-level timeout without an explicit deadline reset.**
- The connect timeout context is created with `connectCtx, cancel := context.WithTimeout(ctx, constants.StreamDeadline)` then `defer cancel()`. But `h.NewStream` is called with `ctx` (the outer context), not `connectCtx`. This means the `NewStream` call uses the full session lifetime context, not the 15s connect timeout. **Is that intentional? A hung `NewStream` won't be bounded by `StreamDeadline`.**

---

## Change 6 — `core/sync/sync_protocols.go` — `HandleAccountsSyncData`

This is the **client-side** stream handler. This is the biggest behavioural change.

### Before
```
stream arrives → read ONE page → write to DB immediately → send ACK → stream closes
```

### After
```
stream arrives → loop:
    read page → ACK immediately → accumulate into batch[]
until EOF → WriteAccountsBatch(all pages) → single DB write → stream closes
```

Full new handler:
```go
var batch []*accountspb.Account

for {
    _ = str.SetReadDeadline(time.Now().Add(constants.DispatchACKTimeout))
    page := &accountspb.AccountSyncServerMessage{}
    if err := pbstream.ReadDelimited(str, page); err != nil {
        break  // EOF = server worker done
    }

    resp := page.GetResponse()
    if resp == nil {
        // send error ACK, continue
        continue
    }

    batch = append(batch, resp.GetAccounts()...)

    ack := accountshelper.NewResultFactory(resp.GetPageIndex()).BatchAck()
    _ = pbstream.WriteDelimited(str, ack)
}

if len(batch) == 0 { return }

s.Datarouter.WriteAccountsBatch(ctx, batch)
```

**Potential issues — this section warrants the most scrutiny:**

1. **ACK is sent before DB write.** The server receives the ACK and considers the page delivered. But the batch is only written to DB after the entire stream closes. If the process crashes between the last ACK and `WriteAccountsBatch`, those accounts are permanently lost with no error surfaced to the server. The server's summary would show them as delivered.
   > **Question for review: is WAL involved here? Is there a WAL write on the client side before ACK?** If not, this is a durability gap compared to the old per-page write.

2. **`batch` grows unbounded in memory.** With `DispatchWorkers=10` workers each sending up to `ceil(N/3000)` pages, one stream carries `ceil(N/10/3000)` pages. For N=10M accounts, one worker sends ~333 pages × 3000 accounts × ~200 bytes ≈ **200MB in a single `batch` slice**. **Is this acceptable memory usage on the receiving node?**

3. **`SetReadDeadline` uses `DispatchACKTimeout = 10s` per page.** If the server worker is slow to send the next page (e.g. DB fetch takes >10s on the server), the client's read deadline fires first, `ReadDelimited` returns an error, and the loop breaks — treating the stream as complete even though the server has more pages. **Is 10s sufficient for the server's DB fetch + serialise + write cycle under load?**

4. **On `WriteAccountsBatch` error**, the handler returns with only a log line. The server has already received all ACKs and thinks everything is delivered. There is no mechanism to signal the server that the client-side batch write failed. **Should there be an error channel or a final error frame on the stream before `str.Close()`?**

5. **`defer str.SetReadDeadline(time.Time{})` and `defer str.SetWriteDeadline(time.Time{})` clear deadlines on exit.** This is fine — libp2p stream is being closed anyway (`defer str.Close()` at the top of the handler). But clearing deadlines before `Close()` is technically a no-op since the stream is about to be torn down. No issue, just noise.

6. **`pageIndex=0` (dead-letter recovery pages)** — the batch accumulator does NOT distinguish between regular pages and recovery pages. They all go into the same `batch`. This is correct — index 0 is just a recovery signal for the server side, the client always just accumulates accounts.

---

## Change 7 — `core/protocol/router/data_router.go`

### What changed

`HandleAccountsSyncData` renamed to `WriteAccountsBatch`:

```go
// Before: HandleAccountsSyncData — per-page write + ACK construction
func (router *Datarouter) HandleAccountsSyncData(ctx, resp, remote) *AccountSyncServerMessage

// After: WriteAccountsBatch — single batch write, no ACK
func (router *Datarouter) WriteAccountsBatch(ctx context.Context, accounts []*accountspb.Account) error
```

`NewDispatcherCallbacks` call updated to pass `OpenAccountsDataStream` + `SendAccountPageOnStream` instead of `StreamAccounts`:

```go
// Before
callbacks := accountshelper.NewDispatcherCallbacks(
    router.Nodeinfo, *remote, router.Comm.StreamAccounts, req.Phase.Auth,
)

// After
callbacks := accountshelper.NewDispatcherCallbacks(
    router.Nodeinfo, *remote,
    router.Comm.OpenAccountsDataStream,
    router.Comm.SendAccountPageOnStream,
    constants.DispatchWorkers,
    req.Phase.Auth,
)
```

**Potential issues:**
- `WriteAccountsBatch` calls `clienthelper.NewClientWriter().SetSyncVars(ctx, *router.Nodeinfo, router.wal)` and then `writer.WriteAccounts(accounts)`. **Verify `WriteAccounts` can handle slices as large as a full worker's page set (potentially tens of thousands of accounts) atomically. Does the underlying immudb write have a batch size cap?**
- `Datarouter` is stateless (per memory rules), so no concern there.

---

## Change 8 — Tests (`tests/accountssync/`)

Three new test files added:

| File | What it tests |
|------|--------------|
| `dispatcher_types_test.go` | Compile-time check: `mockStream` satisfies `AccountSyncStream`; `DispatcherCallbacks` zero value has nil `OpenStream`/`SendPageOnStream` |
| `communication_stream_test.go` | Compile-time check: `communication.NewCommunication` returns a `Communicator` with the two new methods |
| `messaging_stream_test.go` | Integration test: `WriteAccountPageAndReadACK` over a `net.Pipe()` — writes a page, server reads + ACKs, caller verifies ACK ok=true |

**Potential issues:**
- All three tests are **compile-time or minimal wire tests**. There are no tests for:
  - Worker startup failure (`OpenStream` returns error → worker silent exit)
  - Stream reopen path (send failure → `streamOK=false` → reopen)
  - Client-side batch accumulation and `WriteAccountsBatch`
  - Dead-letter recovery path with the new open+send pattern
  - Memory bounds of the client batch under large account counts
- `TestCommunicatorHasStreamMethods` passes `nil` as host to `NewCommunication`. If `NewCommunication` ever does a nil-check and panics, this test would fail. Currently it doesn't — but it's fragile.

---

## Summary — Questions for your review

| # | Area | Question |
|---|------|----------|
| 1 | Client ACK before DB write | If the process crashes between last ACK and `WriteAccountsBatch`, accounts are lost. Is WAL protecting against this? |
| 2 | Client batch memory | ~200MB worst-case per worker stream for 10M accounts. Acceptable on the receiving node? |
| 3 | Read deadline on client loop | `DispatchACKTimeout=10s` per page. Is this enough for server DB fetch + send under load? |
| 4 | No error feedback to server after batch write failure | Server sees all ACKs as success. Should client send an error frame before closing the stream on `WriteAccountsBatch` failure? |
| 5 | Silent worker exit | If `OpenStream` fails at worker startup, the worker exits with no error recorded. Should it log + signal? |
| 6 | Pool connection lifetime | `numWorkers=10` AccountManagers are created at session start and never closed. If `NewAccountManager()` holds a DB connection, those stay open for the full session. Is that fine with jmdn's immudb client? |
| 7 | `NewStream` timeout | `h.NewStream` in `OpenAccountsSyncDataStream` uses the full parent `ctx`, not the 15s connect timeout. A hung stream open won't be bounded. Intentional? |
| 8 | Dead-letter ACK check removed | The explicit `ack.GetBatchAck().GetAck().GetOk()` check was removed from `buildOnDeadLetter`. ACK rejection is now surfaced only via `buildSendPageOnStream` error return. Confirm this still surfaces correctly. |
| 9 | `batchSize=len(nonces)` in pool | `NewAccountNonceIterator(len(nonces))` — was `1` before. Does jmdn's iterator use this value in a way that changes behaviour or memory allocation? |
| 10 | Missing peer ID in send log | `ion.String("from_peer", ...)` removed from `buildSendPageOnStream` success log. Acceptable for debugging? |
