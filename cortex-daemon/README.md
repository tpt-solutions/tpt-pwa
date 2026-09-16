# cortex-daemon (Go)

The tpt-cortex native companion's IPC host ([spec.txt](../spec.txt) §3 Layer 3): a loopback-only WebSocket JSON-RPC server, a persistent task queue, and a connectivity-aware scheduler. Contract: [docs/jsonrpc-contract.md](../docs/jsonrpc-contract.md).

```sh
go vet ./... && go build ./... && go test ./...
go run ./cmd/cortex-daemon -addr 127.0.0.1:9911
```

Flags: `-addr` (default `127.0.0.1:9911`), `-sync-endpoint`, `-poll`, `-max-attempts`, `-queue`, `-data-dir`.

## Packages

| Package | Role |
| --- | --- |
| `internal/queue` | Persistent task queue (atomic JSON file, crash recovery, capped exponential-backoff retries) |
| `internal/rpc` | JSON-RPC 2.0 over WebSocket; strict param validation; broadcast broker for `cortex.event.*` notifications |
| `internal/scheduler` | Drains due tasks whenever the network is up; the PWA can be closed the whole time |
| `internal/syncexec` | Executes `syncNotes` tasks (native twin of the PWA's browser-side flush, spec §4) |

## Security notes

- Binds to `127.0.0.1` by design; no LAN exposure.
- WebSocket origins restricted to loopback patterns.
- `fs.write` is sandboxed: paths are resolved inside `-data-dir` and traversal attempts are rejected.
- Task params are validated strictly (unknown fields rejected) per the "strict, typed contract" in spec §6.

## Planned

Execution routed through `cortex-engine`'s DSL VM (`sync.ctx`) once the daemon embeds the engine binary/FFI — the `scheduler.Executor` interface is the seam (see package comment in `internal/syncexec`).
