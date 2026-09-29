# Example: local background sync, end to end (spec §4 Path A)

This demo runs the complete native loop from a terminal — daemon, DSL engine and sync endpoint — no browser needed. The browser PWA uses the exact same contract (`docs/jsonrpc-contract.md`).

```
cortex-demo (as the PWA)          cortex-daemon                cortex-engine (Rust VM)
  cortex.ping ──────────────────▶ ws://127.0.0.1:9911
  cortex.task.enqueue ──────────▶ persistent queue
                                   scheduler (network up) ────▶ exec-host --script sync.ctx
                                                                  native.db.query ─┐
                                                                  net.isConnected  │ stdio JSON
                                                                  http.post ───────┼─▶ mock endpoint :9999
                                                                  db.exec ─────────┘
  ◀── cortex.event.taskCompleted ──
```

## Run it (one command)

From the repo root:

```sh
sh examples/local-sync/run.sh          # macOS / Linux
./examples/local-sync/run.ps1          # Windows (PowerShell)
```

The script builds the engine, starts the mock endpoint + daemon (engine-backed), enqueues one note, waits for `state: completed`, and tears everything down.

<details>
<summary>Or run the pieces by hand (three terminals, from the repo root)</summary>

```sh
# 0. one-time: build the engine VM (for -engine mode)
cd cortex-engine && cargo build && cd ..

# 1. terminal 1 — stand-in sync endpoint
go run ./cortex-daemon/cmd/cortex-demo serve-mock -addr 127.0.0.1:9999

# 2. terminal 2 — the daemon, executing tasks through the Rust VM
go run ./cortex-daemon/cmd/cortex-daemon \
    -engine "$(go env GOOS 2>/dev/null; ls cortex-engine/target/debug/cortex-engine* | head -1)" \
    -sync-endpoint http://127.0.0.1:9999/sync

# 3. terminal 3 — enqueue one note and watch it flow through
go run ./cortex-daemon/cmd/cortex-demo sync-once \
    -endpoint http://127.0.0.1:9999/sync
```

Expected output in terminal 3: `ping` → `enqueued task <id>` → `cortex.event.taskCompleted` → `final status: ... state: completed`, and terminal 1 logs the pushed note row.

</details>

## Variations

- **Browser PWA instead of terminal 3:** `pnpm dev`, open the app, create a note, and start the daemon as above (with `-sync-endpoint http://127.0.0.1:9999/sync`). The PWA detects the daemon on the loopback WebSocket, hands its outbox to the queue, and the chip flips to "cortex connected".
- **Shared-token auth:** start the daemon with `-auth-token dev-token` and run `sync-once -token dev-token ...`.
- **Without the engine:** omit `-engine` and the daemon uses the built-in Go executor — same wire behavior, different execution core.
