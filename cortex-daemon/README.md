# cortex-daemon (Go)

The tpt-cortex native companion's IPC host ([spec.txt](../spec.txt) §3 Layer 3): a loopback-only WebSocket JSON-RPC server, a persistent task queue, and a connectivity-aware scheduler. Contract: [docs/jsonrpc-contract.md](../docs/jsonrpc-contract.md).

```sh
go vet ./... && go build ./... && go test ./...
go run ./cmd/cortex-daemon -addr 127.0.0.1:9911

# route task execution through the cortex-engine DSL VM (spec §6):
go run ./cmd/cortex-daemon -engine ../cortex-engine/target/debug/cortex-engine
```

Flags: `-addr` (default `127.0.0.1:9911`), `-sync-endpoint`, `-poll`, `-max-attempts`, `-queue`, `-data-dir`, `-engine`, `-engine-script`.

## Packages

| Package | Role |
| --- | --- |
| `internal/queue` | Persistent task queue (atomic JSON file, crash recovery, capped exponential-backoff retries) |
| `internal/rpc` | JSON-RPC 2.0 over WebSocket; strict param validation; broadcast broker for `cortex.event.*` notifications |
| `internal/scheduler` | Drains due tasks whenever the network is up; the PWA can be closed the whole time |
| `internal/syncexec` | Built-in Go executor for `syncNotes` tasks (native twin of the PWA's browser-side flush, spec §4) |
| `internal/engineexec` | Optional executor that runs tasks through the **cortex-engine VM**: the Rust binary executes the DSL script and `native.*` calls round-trip over a line-delimited JSON protocol on stdio (`db.query` serves the task's outbox rows, `net.isConnected` uses the scheduler's probe, `http.post` performs a real POST from the daemon). With `-engine` unset (or a missing binary) the built-in executor is used, so the daemon degrades gracefully. |
| `internal/server` | One assembly point (queue + broker + scheduler + handlers + fs sandbox) shared by the CLI and the mobile binding |
| `mobile/` | gomobile-bindable façade (`Mobile.start/stop`) for embedding in cortex-android's `DaemonService` |

## Security notes

- Binds to `127.0.0.1` by design; no LAN exposure.
- WebSocket origins restricted to loopback patterns.
- `fs.write` is sandboxed: paths are resolved inside `-data-dir` and traversal attempts are rejected.
- Task params are validated strictly (unknown fields rejected) per the "strict, typed contract" in spec §6.
