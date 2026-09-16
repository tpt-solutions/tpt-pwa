# tpt-cortex JSON-RPC Contract

Shared wire contract between the PWA ([`pwa/src/lib/cortex-client.ts`](../pwa/src/lib/cortex-client.ts)), the Go daemon ([`cortex-daemon`](../cortex-daemon)), and the Android companion — one contract, three runtimes (spec §6: "strict, typed contract").

- **Transport:** WebSocket, `ws://127.0.0.1:9911/rpc` (loopback only).
- **Framing:** JSON-RPC 2.0, one request or notification per WebSocket message.
- **Server → client notifications** use bare objects without `id`.
- **Params are strict:** unknown fields are rejected with `-32602`.

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
{ "taskId": "53c281e98e29be47", "state": "queued" }
```

Also accepted: `"payload": <any>` instead of `entries`, and optional `"runAt": <RFC 3339>` to defer. Params require exactly one of `entries`/`payload`; `kind` is mandatory.

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

### `fs.write` — native file save (spec §6 example)

```jsonc
// params: { "path": "exports/note.txt", "buffer": "<base64>" }
// result: { "path": "<absolute path>", "bytesWritten": 42 }
```

Paths are resolved inside the daemon's data directory; traversal attempts return `-32602`.

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
   - **Path A** — daemon connected: `cortex.task.enqueue {kind:"syncNotes", entries}` → success empties the outbox; the daemon's scheduler pushes to the sync endpoint whenever the OS reports connectivity, even with no tab open.
   - **Path B** — no daemon: direct HTTP push per entry while online; failures stay queued for the next `online` event / app focus / Background Sync tick.
3. Daemon notifies `cortex.event.taskCompleted` so a connected PWA can refresh.
