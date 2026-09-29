# tpt-cortex JSON-RPC Contract

Shared wire contract between the PWA ([`pwa/src/lib/cortex-client.ts`](../pwa/src/lib/cortex-client.ts)), the Go daemon ([`cortex-daemon`](../cortex-daemon)), and the Android companion — one contract, three runtimes (spec §6: "strict, typed contract").

- **Transport:** WebSocket, `ws://127.0.0.1:9911/rpc` (loopback only).
- **Framing:** JSON-RPC 2.0, one request or notification per WebSocket message.
- **Server → client notifications** use bare objects without `id`.
- **Params are strict:** unknown fields are rejected with `-32602`.
- **Auth (optional):** when the daemon runs with `-auth-token <t>`, every upgrade must present the token as the `token` query parameter (`ws://127.0.0.1:9911/rpc?token=t` — the browser-friendly path, since WebSocket APIs cannot set headers) or an `X-Cortex-Token` header; otherwise the upgrade is answered `401`. Unset (default) means loopback-trust per spec §3.

## Error codes

| Code | Meaning |
| --- | --- |
| `-32700` | message was not valid JSON |
| `-32600` | not a JSON-RPC 2.0 request / missing method |
| `-32601` | method not found |
| `-32602` | invalid params (bad shape, unknown field, missing required field) |
| `-32000` | server-side execution failure |

## Methods

### `cortex.ping` → capability probe

```jsonc
// request
{ "jsonrpc": "2.0", "id": 1, "method": "cortex.ping" }
// result
{ "pong": true, "version": "0.1.0" }
```

The PWA calls this implicitly via `checkCortexConnection()`; a successful handshake sets `window.cortexConnected = true` (spec §3/§4).

### `cortex.task.enqueue` — queue a background task

```jsonc
// request (syncNotes shape used by the PWA's CortexSyncTransport)
{
  "jsonrpc": "2.0", "id": 2, "method": "cortex.task.enqueue",
  "params": {
    "kind": "syncNotes",
    "entries": [
      { "id": "note-1:create", "kind": "note", "action": "create",
        "payload": { "id": "note-1", "title": "Hi", "body": "...",
                     "createdAt": 1700000000000, "updatedAt": 1700000000000,
                     "syncedAt": null },
        "queuedAt": 1700000000000 }
    ]
  }
}
// result
{ "taskId": "53c281e98e29be47", "state": "queued", "accepted": 1, "deduplicated": false }
```

Also accepted: `"payload": <any>` instead of `entries`, and optional `"runAt": <RFC 3339>` to defer. Params require exactly one of `entries`/`payload`; `kind` is mandatory and must be one of `syncNotes` or `crdtMerge` (anything else is `-32602`).

`"batchId": "<string>"` is an optional idempotency key: re-submitting a batch whose queued/running task already carries that key returns the SAME task with `"deduplicated": true` instead of queueing it twice. The PWA derives the key from the batch contents, so a flush retried after a lost response dedupes.

`"accepted"` counts the entries in the submission so clients can verify the daemon acknowledged the whole batch (a mismatched count is treated as a failed hand-off and the client degrades to the fallback path).

### `cortex.task.status`

```jsonc
// params: { "taskId": "53c281e98e29be47" }
// result
{ "taskId": "…", "kind": "syncNotes", "state": "completed", "attempts": 1,
  "runAt": "…", "createdAt": "…", "updatedAt": "…", "lastError": "" }
```

`state` ∈ `queued | running | completed | failed`. Failed tasks were retried `-max-attempts` times (exponential backoff) before being parked.

### `cortex.task.list`

```jsonc
// params: none; result: { "tasks": [ …same objects as status… ] }
```

### `cortex.task.cancel` — park a queued task

```jsonc
// params: { "taskId": "53c281e98e29be47" }
// result: the task object, now state "failed" with lastError "cancelled"
```

Only **queued** tasks can be cancelled; running tasks refuse (`-32602`) because the executor's result decides their terminal state.

### `cortex.task.retry` — requeue a failed task

```jsonc
// params: { "taskId": "53c281e98e29be47" }
// result: the task object, back to state "queued" with attempts reset to 0
```

### `cortex.task.prune` — drop finished tasks

```jsonc
// params: none (or null); result: { "pruned": 3 }
```

Removes every `completed`/`failed` task from the queue file; pending work is never touched.

## Plain HTTP endpoints (same loopback server, not JSON-RPC)

- `GET /health` — liveness: `{"status":"ok","version":"…","queue":{queued,running,completed,failed}}`.
- `GET /metrics` — Prometheus text format: `cortex_queue_tasks{state}`, `cortex_tasks_total{kind=enqueued|completed|failed}`, `cortex_uptime_seconds`.

Both are read-only and inherit the daemon's loopback binding; they are not gated by `-auth-token`.

### `fs.write` — native file save (spec §6 example)

```jsonc
// params: { "path": "exports/note.txt", "buffer": "<base64>" }
// result: { "path": "<absolute path>", "bytesWritten": 42 }
```

Paths are resolved inside the daemon's data directory — **after** symlink resolution, so a symlink (or Windows junction) inside the sandbox pointing elsewhere cannot become an escape hatch; traversal attempts return `-32602`. Decoded payloads larger than 4 MiB are rejected with `-32602`. Writes land atomically via temp file + rename: a crash mid-write leaves the previous contents intact.

The sync endpoint's push envelope carries the outbox entry's id (see §"End-to-end", step 2) so server-side deduplication can recognise retried pushes of the same entry.

> Not part of this contract: the *internal* line-delimited JSON protocol between the daemon and the cortex-engine VM (`exec-host`), documented in `cortex-engine/src/host.rs`.

## Server → client notifications

### `cortex.event.taskCompleted`

```jsonc
{ "jsonrpc": "2.0", "method": "cortex.event.taskCompleted",
  "params": { "taskId": "53c281e98e29be47", "state": "completed" } }
```

Broadcast when a task reaches `completed` or `failed`. Subscribe with `cortexRPC.onNotification('cortex.event.taskCompleted', …)`.

## End-to-end: the spec §4 sync scenario

1. User creates a note offline → PWA writes it to SQLite/IndexedDB and the durable outbox (optimistic UI never waits).
2. `SyncManager.flush()` checks the negotiated path:
   - **Path A** — daemon connected: `cortex.task.enqueue {kind:"syncNotes", batchId, entries}` → success empties the outbox; the daemon's scheduler pushes to the sync endpoint whenever the OS reports connectivity, even with no tab open. Each push envelope is `{ "id": "<outbox entry id>", "action": "…", "note": {…} }`.
   - **Path B** — no daemon: direct HTTP push per entry while online; failures stay queued for the next `online` event / app focus / Background Sync tick. Permanent 4xx responses (any 4xx except 408/429) dead-letter the entry instead of retrying forever. When no endpoint is configured, Path B is disabled and entries wait for the daemon.
3. Daemon notifies `cortex.event.taskCompleted` so a connected PWA can refresh.
