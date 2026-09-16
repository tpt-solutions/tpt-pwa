# tpt-pwa — TODO

Tracks implementation progress against [spec.txt](spec.txt). Monorepo layout: `pwa/` (Svelte+TS), `cortex-daemon/` (Go), `cortex-engine/` (Rust), `cortex-shell/` (Tauri), `cortex-android/`. License: `MIT OR Apache-2.0` (TPT Solutions) — see [LICENSE-MIT](LICENSE-MIT) / [LICENSE-APACHE](LICENSE-APACHE).

## Phase 0 — Repo Bootstrap

- [x] Dual license files (`LICENSE-MIT`, `LICENSE-APACHE`), copyright TPT Solutions
- [x] Root `README.md` with architecture summary
- [x] `pnpm-workspace.yaml` for JS/TS packages
- [x] `.gitignore`
- [x] `git init` + first commit
- [x] GitHub Actions CI skeleton (lint/build per package, path-filtered by language)

## Phase 1 — Core PWA & Fallbacks

- [x] Scaffold `pwa/` with Vite + plain Svelte + TypeScript
- [x] Custom minimal Service Worker generator (cache-first routing, ~2KB target, no Workbox) — `pwa/scripts/generate-sw.mjs`, emits 1.9KB worker from the build manifest; relays Background Sync (`tpt-sync`) to the app
- [x] Integrate `wa-sqlite` (SQLite via Wasm) for local queries — SQLite on the OPFS VFS, lazy-loaded
- [x] IndexedDB fallback storage path — plus in-memory last resort; one `NoteStorage` interface (`pwa/src/lib/storage/`)
- [x] Svelte stores for local state management — `pwa/src/lib/stores.ts`
- [x] `checkCortexConnection()` feature-detection stub — real probe in `pwa/src/lib/cortex-client.ts`; sets `window.cortexConnected`, never per-OS
- [x] Capability-negotiation pattern: single code path branching only on cortex availability, never per-OS — storage negotiation (`createStorage`) + cortex path A/B in `SyncManager`
- [x] PWA manifest, install prompts, offline-first app shell
- [x] View Transitions API integration — list ⇄ editor routes via `document.startViewTransition`
- [x] Optimistic UI updates (note creation scenario from spec §4) — with rollback on failed persistence; debounced durable writes
- [x] Automerge-rs (CRDT) via Wasm — conflict-resolution scaffold for future multi-device sync (`pwa/src/lib/crdt.ts`, merge roundtrip tested)

## Phase 2 — Cortex Daemon Integration

- [x] `cortex-daemon` (Go): WebSocket server on `127.0.0.1:9911` — JSON-RPC 2.0, strict params, loopback origin allow-list
- [x] `cortex-daemon`: persistent task queue + scheduler (`cortex.task.enqueue`) — atomic JSON persistence, crash recovery, capped exponential backoff, connectivity-gated execution
- [x] `cortex-engine` (Rust): bytecode VM skeleton — lex → parse → compile → stack VM; instruction budget; `examples/sync.ctx` runs end-to-end
- [x] `cortex-engine`: native bindings (`native.db`, `native.net`, `native.http`) — `NativeEnv` trait + registry + deterministic test double
- [ ] `cortex-daemon` executing tasks through the cortex-engine VM — daemon currently executes `syncNotes` natively in Go (`internal/syncexec`); `scheduler.Executor` is the seam for embedding the engine (FFI or subprocess)
- [x] Document the JSON-RPC contract shared between PWA and daemon (methods, payloads) — [docs/jsonrpc-contract.md](docs/jsonrpc-contract.md)
- [x] PWA-side WebSocket RPC client (`src/lib/cortex-client.ts`), `window.cortexConnected` flag
- [x] Migrate background sync end-to-end (Path A hand-off to daemon + Path B browser fallback both tested; spec §6 `sync.ctx` proven in the engine)
- [x] Fallback path test: WebSocket failure → Service Worker + IndexedDB + `online` event listener sync — `pwa/src/lib/sync.test.ts`

## Phase 3 — Android Companion

- [x] `cortex-android` project scaffold — Gradle 8.9 wrapper committed; Kotlin service/bridge/activity
- [ ] Run `cortex-daemon` as a foreground/background Android service — `DaemonService` (foreground, `dataSync`) + reconnecting `CortexBridge` scaffold in place; gomobile bind of the daemon is the next milestone
- [ ] Direct APK download distribution flow (no Play Store)
- [ ] Bridge PWA ↔ Android service over the existing WebSocket/JSON-RPC contract — contract-speaking bridge skeleton exists; task dispatch/notification routing pending
- [x] `cortex-shell` (Tauri) desktop installer bundling the Go daemon — Tauri 2 scaffold, sidecar config + prepare script, `cargo check` clean; release bundling needs a built daemon binary
- [x] Register custom `tpt://` protocol in `cortex-shell` — deep-link plugin (`schemes: ["tpt"]`) + Android intent filter

## Phase 4 — Formal Verification

- [x] Evaluate `tpt-telos` formal verification methodology for applicability (external dependency, not built here) — [docs/formal-verification.md](docs/formal-verification.md) §3
- [x] Apply verification to `cortex-engine` for memory-safety / crash-resistance guarantees — layer 1 executable guarantees in place (`tests/safety.rs`: adversarial bytecode, budget bounds, hostile natives); Kani model checking is the planned layer 2
- [x] Document verification scope and approach in `docs/`

## Cross-cutting

- [x] Add `"license": "MIT OR Apache-2.0"` to `package.json` files once they exist
- [x] Add `license = "MIT OR Apache-2.0"` to `Cargo.toml` files once they exist
- [x] Contribution guidelines noting dual-license terms for external PRs — [CONTRIBUTING.md](CONTRIBUTING.md)
