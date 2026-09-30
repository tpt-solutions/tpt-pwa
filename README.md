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
- **Try the task language in your browser:** run the PWA and open the **.ctx playground** (header button) — the real cortex-engine VM compiled to WebAssembly, running the same bytecode as the daemon against a deterministic sandbox. The artifact is committed; after changing `cortex-engine/`, regenerate it with `pnpm run engine:build` (Rust + wasm-pack) and commit.
- **Build your own app:** fork the repo and adapt `pwa/`. It works offline with Service Workers + IndexedDB alone, and gains background processing and hardware access when a companion is connected.
- **Add a native companion:** `node tools/create-tpt-companion --name cortex-foo --lang go|rust|ts` ([docs](tools/create-tpt-companion/README.md)). Companions speak the [JSON-RPC contract](docs/jsonrpc-contract.md).
- **Learn by example:** [examples/local-sync](examples/local-sync/README.md) and [examples/multi-device-sync](examples/multi-device-sync/README.md).

Progress is tracked in [TODO.md](TODO.md). This project accepts **issues only, not pull requests** — see [CONTRIBUTING.md](CONTRIBUTING.md). The shared [JSON-RPC contract](docs/jsonrpc-contract.md) is the reference for the wire protocol. Formal-verification scope for the engine lives in [docs/formal-verification.md](docs/formal-verification.md).

## Architecture

```mermaid
flowchart LR
    subgraph browser["Browser / WebView (untrusted by design)"]
        PWA["tpt-pwa (Svelte + TS)<br/>optimistic UI · outbox · CRDT mirror"]
        SW["Service Worker (~2KB)<br/>cache-first shell + sync relay"]
        PWA --- SW
    end

    subgraph native["Local native layer (the only OS-trusted side)"]
        D["cortex-daemon (Go)<br/>JSON-RPC 2.0 · persistent queue · scheduler"]
        E["cortex-engine (Rust)<br/>.ctx bytecode VM"]
        D -- "stdio host protocol<br/>(native.db / net / http)" --> E
    end

    subgraph hosts["Companion hosts"]
        A["cortex-android<br/>foreground service + bridge"]
        T["cortex-shell (Tauri)<br/>desktop shell + sidecar"]
    end

    PWA -- "ws://127.0.0.1:9911/rpc<br/>(loopback JSON-RPC, optional token)" --> D
    A --> D
    T --> D
    D -- "HTTPS push<br/>(sync endpoint, with retry/dead-letter)" --> S["Your sync backend"]
    E -. "runs each task's script" .-> D

    click "https://github.com/tpt-solutions/tpt-pwa/blob/master/docs/jsonrpc-contract.md" "JSON-RPC contract"
```

Capability negotiation is the framework's core rule: the PWA has exactly ONE branch point — "is the daemon reachable?" — and degrades to Service Worker + IndexedDB + background sync when it isn't. There is no per-OS code anywhere in `pwa/`.

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
| `-log-format` / `-log-level` | `text` / `info` | `json` emits one parseable object per line (levels, `component` attrs) for systemd/Docker scraping |
| `-allow-natives` | _(unset = all)_ | Capability allowlist for engine task scripts (e.g. `"db.query,net.isConnected"`); a script whose static manifest exceeds it parks as failed without executing any native call |
| `-config` | _(unset)_ | JSON config file; keys mirror the flags (`{"addr": "127.0.0.1:9911", "log-format": "json", ...}`), every explicit flag overrides it, unknown keys are rejected |

There is no `.env` indirection on purpose: the daemon is flag-driven with an optional JSON config file for persistence (see `go run ./cortex-daemon/cmd/cortex-daemon -h`) and the PWA needs no configuration. If you put `-auth-token` in a config file, protect it like a secret (file permissions, not in a repo).

Before blaming the daemon, run its pre-flight check: `go run ./cortex-daemon/cmd/cortex-daemon doctor` (port, origin, auth, queue, data-dir, engine, sync endpoint; `--json` for tooling). More help: [docs/troubleshooting.md](docs/troubleshooting.md).

Install the git hooks (pre-commit mirror of CI): `pnpm run hooks:setup`.

### Documentation map

| Doc | Contents |
| --- | --- |
| [docs/jsonrpc-contract.md](docs/jsonrpc-contract.md) | The wire protocol shared by PWA, daemon, and Android bridge |
| [docs/language-reference.md](docs/language-reference.md) | The `.ctx` DSL: syntax, types, natives, budgets |
| [docs/troubleshooting.md](docs/troubleshooting.md) | Port 9911, Android WebView cleartext, Windows firewall, corrupt queues |
| [examples/recipes/](examples/recipes/) | Copy-paste `.ctx` recipes with tests (retry-upload, periodic fetch, batch sync) |
| [docs/formal-verification.md](docs/formal-verification.md) | What "safe to run hostile scripts" means here, and how it's checked |
| [SECURITY.md](SECURITY.md) | Trust boundaries, token handling, reporting process |
| [CHANGELOG.md](CHANGELOG.md) | Notable changes per release |

## License

Licensed under either of

- MIT license ([LICENSE-MIT](LICENSE-MIT))
- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))

at your option. SPDX identifier: `MIT OR Apache-2.0`.

Copyright (c) 2026 TPT Solutions.
