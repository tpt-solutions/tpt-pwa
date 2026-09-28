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
- [x] `cortex-daemon` executing tasks through the cortex-engine VM — `-engine` flag runs the Rust VM via `internal/engineexec`: the script's `native.*` calls round-trip over a stdio JSON protocol to real daemon effects; tested against a fake engine and the real binary (`CORTEX_ENGINE_BIN` e2e)
- [x] Document the JSON-RPC contract shared between PWA and daemon (methods, payloads) — [docs/jsonrpc-contract.md](docs/jsonrpc-contract.md)
- [x] PWA-side WebSocket RPC client (`src/lib/cortex-client.ts`), `window.cortexConnected` flag
- [x] Migrate background sync end-to-end (Path A hand-off to daemon + Path B browser fallback both tested; spec §6 `sync.ctx` proven in the engine)
- [x] Fallback path test: WebSocket failure → Service Worker + IndexedDB + `online` event listener sync — `pwa/src/lib/sync.test.ts`

## Phase 3 — Android Companion

- [x] `cortex-android` project scaffold — Gradle 8.9 wrapper committed; Kotlin service/bridge/activity
- [x] Run `cortex-daemon` as a foreground/background Android service — `DaemonService` (foreground, `dataSync`) loads the gomobile-bound daemon (`cortex-daemon/mobile` → `Mobile.start/stop`, reflectively; the .aar is built at packaging time per README) and keeps liveness even without it
- [x] Direct APK download distribution flow (no Play Store) — `.github/workflows/release.yml` attaches release/debug APKs to GitHub releases on `v*` tags; `pwa/public/companion.html` is the download page
- [x] Bridge PWA ↔ Android service over the existing WebSocket/JSON-RPC contract — PWA in the WebView connects directly to the loopback daemon (network security config), `CortexBridge` is a full id-matched JSON-RPC client with `enqueueSync` + notification relay into the WebView (`cortex:notification` events, consumed by the PWA); JVM unit tests
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

## Platform Review Follow-ups (2026-09-27)

Bugs, security hardening, and DX/adoption gaps found in a full-platform review. Ordered by priority.

- [x] Clean up repo noise — `exp1.txt` and `pwa/zz-marker.txt` deleted
- [x] Fix outbox flush overwrite bug — `pwa/src/lib/sync.ts` `#markSynced` now only stamps `syncedAt` when storage holds the revision this entry synced (`stored.updatedAt <= payload.updatedAt`); it never copies stale payload content back, so a newer local edit survives; regression test added (`sync.test.ts` "never clobbers a newer local edit")
- [x] Surface swallowed errors in dev — `pwa/src/lib/devlog.ts` `warnDev(scope, error)` (a `console.warn` gated on `import.meta.env.DEV`) is wired into every degradation path: sync flush, CRDT load/open/merge, SW registration, storage negotiation, app bootstrap/rollback
- [x] Wire Android connection-state callback — `DaemonService` broadcasts `CortexBridge.State` through its binder to `MainActivity`, which relays it into the WebView as a `cortex:state` DOM event; the PWA updates its cortex capability chip and re-arms the sync transport on `connected`
- [x] Harden daemon RPC endpoint — optional `-auth-token` enforces a shared token on `/rpc` upgrades via `token` query param (browser-friendly) or `X-Cortex-Token` header, constant-time compared, 401 otherwise; `fs.write` rejects decoded payloads above 4 MiB; both covered in `internal/server/hardening_test.go` and documented in `docs/jsonrpc-contract.md`
- [x] Add a root-level quickstart — root `package.json` scripts (`pnpm dev`, `pnpm run dev:daemon`, `pnpm test`), README quickstart section with the daemon flag table (no `.env` indirection on purpose — the daemon is flag-driven)
- [x] Build a real end-to-end example — `examples/local-sync` runs daemon → engine VM → mock endpoint end to end, driven by the new `cortex-daemon/cmd/cortex-demo` CLI (`serve-mock` + `sync-once`), which doubles as a non-browser contract client
- [x] Add local pre-commit automation mirroring CI checks — `scripts/githooks/pre-commit` (svelte-check + vitest, gofmt + go vet, `cargo fmt --check`), installed via `pnpm run hooks:setup`
- [x] Wire `cortex-daemon` to execute tasks through the `cortex-engine` VM — done: `internal/engineexec` + `-engine` flag (see Phase 2)
- [x] Fill test gaps — `cortex-engine` now has per-module unit tests (lexer tokens/positions/errors, parser precedence/AST shape, compiler slot allocation/jump patching/native resolution, VM arithmetic/overflow/member access/budget bounds), plus baseline tests: `cortex-shell` pins the tpt:// scheme + daemon sidecar in `tauri.conf.json`, `cortex-android` pins the contract URL (JVM unit tests, run in CI)

## Former "Ideas for later" — now built

- [x] Scaffold CLI — `tools/create-tpt-companion` (`pnpm scaffold -- --name cortex-thing --lang go|rust|ts`): generates a companion package with copyright headers, manifest license fields, a contract-shaped starter and a passing test; verified that generated Go and Rust output compiles and tests green, and that TS companions auto-register in `pnpm-workspace.yaml`
- [x] Multi-device sync demo — `examples/multi-device-sync` + `pwa/src/lib/crdt-multi-device.test.ts`: two devices diverge from a shared state and edit the same note's different fields offline, then converge with neither write lost. Building it exposed and fixed two real `crdt.ts` bugs: notes now live directly in the document root (a `_root.notes` container could win/lose wholesale on concurrent first-creation) and `upsertNote` reuses a note's field map so merges are field-level instead of whole-note
- [x] Status/telemetry panel — a collapsible panel in the PWA (`App.svelte`) showing daemon identity/version (`cortex.ping`), the daemon's task queue shape (`cortex.task.list`, summarized by `telemetry.ts`), local outbox depth, the last flush outcome, storage backend, and CRDT readiness; refreshed on open, on (re)connect, with unit tests
