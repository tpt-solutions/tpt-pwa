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

## Run it

Three terminals, from the repo root:

```sh
# 0. one-time: build the engine VM (for -engine mode)
cd cortex-engine && cargo build && cd ..

# 1. terminal 1 — stand-in sync endpoint
go run ./cortex-daemon/cmd/cortex-demo serve-mock -addr 127.0.0.1:9999

# 2. terminal 2 — the daemon, executing tasks through the Rust VM
go run ./cortex-daemon/cmd/cortex-daemon \
    -engine ./cortex-engine/target/debug/cortex-engine.exe \
    -sync-endpoint http://127.0.0.1:9999/sync

# 3. terminal 3 — enqueue one note and watch it flow through
go run ./cortex-daemon/cmd/cortex-demo sync-once \
    -endpoint http://127.0.0.1:9999/sync
```

Expected output in terminal 3: `ping` → `enqueued task <id>` → `cortex.event.taskCompleted` → `final status: ... state: completed`, and terminal 1 logs the pushed note row. (On Windows keep the `.exe` suffix; on other platforms drop it.)

## Variations

- **Browser PWA instead of terminal 3:** `pnpm dev`, open the app, create a note, and start the daemon as above (with `-sync-endpoint http://127.0.0.1:9999/sync`). The PWA detects the daemon on the loopback WebSocket, hands its outbox to the queue, and the chip flips to "cortex connected".
- **Shared-token auth:** start the daemon with `-auth-token dev-token` and run `sync-once -token dev-token ...`.
- **Without the engine:** omit `-engine` and the daemon uses the built-in Go executor — same wire behavior, different execution core.
