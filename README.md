# tpt-pwa

An offline-first, app-store-free web application framework. tpt-pwa pairs a lightweight Svelte/TypeScript frontend with the optional `tpt-cortex` native companion to deliver true background processing, persistent local storage, and hardware access — while gracefully degrading to standard Web APIs (Service Workers + IndexedDB) when the native companion isn't available. See [spec.txt](spec.txt) for the full design document.

## Monorepo layout

```
tpt-pwa/
├── pwa/                 # Layer 1: Svelte + TS frontend (Vite, plain Svelte)
├── cortex-daemon/       # Layer 3: Go IPC host / WebSocket server / scheduler
├── cortex-engine/       # Layer 3: Rust DSL VM, bytecode executor
├── cortex-shell/        # Tauri-based desktop installer bundling the daemon
├── cortex-android/      # Android companion (runs cortex-daemon as a service)
├── docs/                # Design docs, ADRs
├── LICENSE-MIT
├── LICENSE-APACHE
└── TODO.md              # Phased task checklist
```

## Who is this for / how to use it

tpt-pwa is a **framework you fork or scaffold from**, not a packaged end-user app. There are no published packages or releases yet.

- **Try it:** follow the Quickstart below. The PWA runs standalone; the daemon is optional.
- **Build your own app:** fork the repo and adapt `pwa/`. It works offline with Service Workers + IndexedDB alone, and gains background processing and hardware access when a companion is connected.
- **Add a native companion:** `node tools/create-tpt-companion --name cortex-foo --lang go|rust|ts` ([docs](tools/create-tpt-companion/README.md)). Companions speak the [JSON-RPC contract](docs/jsonrpc-contract.md).
- **Learn by example:** [examples/local-sync](examples/local-sync/README.md) and [examples/multi-device-sync](examples/multi-device-sync/README.md).

Progress is tracked in [TODO.md](TODO.md). This project accepts **issues only, not pull requests** — see [CONTRIBUTING.md](CONTRIBUTING.md). The shared [JSON-RPC contract](docs/jsonrpc-contract.md) is the reference for the wire protocol. Formal-verification scope for the engine lives in [docs/formal-verification.md](docs/formal-verification.md).

## Quickstart

Prerequisites: Node 22+, pnpm 11, Go 1.25, Rust 1.85+ (stable).

```sh
pnpm install            # workspace + pwa deps
pnpm test               # PWA (vitest), daemon (go test), engine (cargo test)
pnpm dev                # PWA dev server  → http://localhost:5173
pnpm run dev:daemon     # cortex-daemon   → ws://127.0.0.1:9911/rpc
```

With both running, the app's header chip flips to **cortex connected** and notes sync through the daemon's persistent queue. Full walk-through with the DSL engine and a mock sync endpoint: [examples/local-sync](examples/local-sync/README.md).

### Daemon configuration (flags)

| Flag | Default | Purpose |
| --- | --- | --- |
| `-addr` | `127.0.0.1:9911` | Listen address (loopback by design) |
| `-sync-endpoint` | `https://api.tpt/sync` | Where queued notes are pushed |
| `-engine` | _(unset)_ | cortex-engine binary → tasks run through the DSL VM instead of the built-in Go executor |
| `-engine-script` | embedded `sync.ctx` | Alternate DSL script for `-engine` |
| `-auth-token` | _(unset)_ | Require a shared token on `/rpc` upgrades (`?token=` or `X-Cortex-Token`); browsers should use the query param |
| `-queue` | OS config dir | Persistent task queue file |
| `-data-dir` | OS config dir | Sandbox root for `fs.write` |
| `-poll` / `-max-attempts` | `5s` / `8` | Scheduler cadence and retry ceiling |

There is no `.env` indirection on purpose: the daemon is flag-driven (see `go run ./cortex-daemon/cmd/cortex-daemon -h`) and the PWA needs no configuration.

Install the git hooks (pre-commit mirror of CI): `pnpm run hooks:setup`.

## License

Licensed under either of

- MIT license ([LICENSE-MIT](LICENSE-MIT))
- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))

at your option. SPDX identifier: `MIT OR Apache-2.0`.

Copyright (c) 2026 TPT Solutions.
