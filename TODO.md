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

## Platform Review 2 (2026-09-29)

Findings from a second full review (PWA, daemon, engine, adoption). Found by reading code, not by running it: write a failing regression test or repro first, then fix. Detail lives in the approved plan. Tier 1 = bugs, Tier 2 = adoption, Tier 3 = features/ideas.

### Tier 1 — CI/tooling bugs

- [x] Fix CI PR path filters — `ci.yml` used `pull_request.changed_files` (an integer count), so PR jobs never ran; replaced with a `changes` job using `dorny/paths-filter`, and the PWA job now runs `pnpm run test` (edit applied, **unverified until a PR runs it**)
- [ ] Verify the CI fix with a throwaway PR touching only `pwa/` (pwa job runs, others skip)
- [ ] Make `cortex-shell/scripts/prepare-sidecar.mjs` fail loudly instead of writing a 0-byte sidecar; have CI run it
- [ ] Sign the release APK and build the gomobile `.aar` in `release.yml` (today's APK is unsigned and ships without the daemon)
- [ ] Check `cortex-android/app/proguard-rules.pro` keeps the reflectively loaded `Mobile` class (R8 is on)
- [ ] Verify the root `dev:daemon` script (`go.mod` lives in `cortex-daemon/`)
- [ ] Add `.gitattributes` (`* text=auto`; `gradlew` and `*.sh` as LF)
- [ ] Scaffold CLI fixes: Go template test shadows `status()`, silent workspace-regex failure, TS template lacks vitest/typescript devDeps, add `--dry-run`/`--force`

### Tier 1 — PWA bugs

- [ ] Persist and load the CRDT — `app.ts:34` opens `NoteDoc` with no binary and never saves; save after each mutation, load on open, seed from `listNotes()` on first run
- [ ] Flush debounced edits on `pagehide`/`visibilitychange`; clear the timer in `deleteNote`
- [ ] Fix rollback: restore the right snapshot and undo the storage write when `createNote` rolls back the UI
- [ ] Make sync hand-off idempotent — idempotency keys, independent dequeues (no `Promise.all`), monotonic outbox ordering instead of `Date.now()`
- [ ] Add tombstones for deletes in `Note` and the CRDT (a delete currently loses to a concurrent edit or resurrects)
- [ ] Make `#markSynced` one atomic storage op (`UPDATE ... WHERE updatedAt <= ?`)
- [ ] Storage backend switching: persist the chosen backend, migrate on switch, elect a leader tab (Web Locks) so a second tab doesn't silently fall back to a different database
- [ ] `CortexRPC`: reconnect with backoff, reject pending calls on close, memoize in-flight `connect()`, check `enqueueSyncTask` results
- [ ] Service worker: exclude `/sw.js` from the fetch handler, hash file contents (not names/sizes), replace mid-session `skipWaiting()` with an update-available prompt
- [ ] HTTP sync path: dead-letter permanent 4xx entries; replace the placeholder default endpoint `https://api.tpt/sync`
- [ ] Smaller PWA fixes: unhandled rejections in `App.svelte`, `installPrompt` timing/clearing, `modulePromise` caching a null result, wasm handle leak in `crdt.merge`, device-clock LWW ordering
- [ ] PWA test gaps: `app.ts` (debounce/rollback), `cortex-client.ts`, the three storage backends, `generate-sw.mjs`, CRDT persist/reload, multi-tab

### Tier 1 — Daemon (Go) bugs

- [ ] `scheduler.go`/`server.go`: `OnTransition` receives the requested state, so `failed` never broadcasts `taskCompleted`; report the resulting state
- [ ] `cmd/cortex-daemon/main.go`: no signal handling; use `signal.NotifyContext` for SIGINT/SIGTERM
- [ ] `engineexec.go`: deadlock when the engine stays alive after a decode error; kill it before `Wait`
- [ ] Add a per-task timeout and an `http.Client` timeout (`engineexec.go`, `syncexec.go`) so one hung task can't stall the serial queue
- [ ] `server.go`: raise the WebSocket read limit above the base64 size of a 4 MiB payload so the documented `-32602` actually fires
- [ ] `fs.write`: resolve symlinks (`EvalSymlinks`) to close the sandbox escape; write via temp file plus rename
- [ ] `rpc.Broadcast`: don't write while holding the lock; add keepalive pings
- [ ] `queue.go`: fsync, prune finished tasks, move a corrupt file aside instead of refusing to start, roll back the in-memory append when the save fails
- [ ] Graceful shutdown: `Run` should wait for `Shutdown` and the scheduler
- [ ] Reject unknown task kinds at enqueue, classify permanent vs transient failures, add idempotency keys to retries, enforce exactly one of `entries`/`payload`
- [ ] Remove the dead `127.0.0.0/8:*` origin pattern; require an auth token in `mobile.go`

### Tier 1 — Engine (Rust) bugs

- [ ] Depth limit and token cap in the parser/compiler/`Drop` (deep nesting overflows the stack, contradicting the "total" claim in `lib.rs`); add adversarial tests
- [ ] Compiler jump targets cast `as u16` wrap silently; use `try_from` and return a compile error
- [ ] Fix quadratic `LoadLocal` list cloning that the instruction budget doesn't count
- [ ] Distinct exit codes (2 = permanent) so the daemon stops retrying parse/compile errors 8 times
- [ ] Short-circuit `&&`/`||`; fix NaN and Int/Float comparison inconsistencies and the wrong error variant for data errors

### Tier 2 — Adoption

- [ ] Release binaries for daemon and engine (win/mac/linux, x64+arm64) with SHA256 checksums, `--generate-notes`, and a tests-first gate
- [ ] One-line installers (`install.ps1`/`install.sh`), then winget/scoop/Homebrew manifests
- [ ] `cortex doctor` subcommand (port, token, origin, engine binary/version, queue health), linked from the PWA cortex chip
- [ ] Devcontainer plus `mise.toml`/`.tool-versions` (Node 22, pnpm 11, Go 1.25, Rust 1.85); Dockerfile for the daemon
- [ ] Make `examples/local-sync` and `examples/multi-device-sync` runnable with one command; remove hardcoded `.exe` paths
- [ ] `create-tpt-pwa` starter (notes/todo/chat templates with the daemon-degradation layer and CI prewired); register scaffolded Go/Rust companions in CI
- [ ] `.ctx` recipe cookbook in `examples/recipes/` (retry-upload, periodic fetch, batch sync), each with a test
- [ ] `SECURITY.md` (loopback binding, origin rules, token handling, `fs` sandbox, engine budget, reporting process)
- [ ] Architecture diagram (Mermaid) in the README
- [ ] `.ctx` language reference
- [ ] Troubleshooting/FAQ (port 9911, Android cleartext-loopback WebView, Windows firewall)
- [ ] CHANGELOG, `CODE_OF_CONDUCT.md`, issue/PR templates, `CODEOWNERS`; expand `cortex-shell/README.md`, `CONTRIBUTING.md`, `docs/jsonrpc-contract.md`
- [ ] CI: Windows/macOS matrix, Go and Gradle caches, Dependabot, CodeQL, `govulncheck`, `cargo audit`, `pnpm audit`, version-sync check across all manifests, coverage, provenance attestations

### Tier 3 — Features and ideas (menu, unprioritized)

- [ ] Product gaps: search, export/import (JSON/Markdown), `navigator.storage.persist()`, encryption at rest and in sync, multi-tab coordination, register Background Sync (`tpt-sync` is never registered, so the SW handler is dead code), a11y (live regions, aria-labels, reduced motion), i18n
- [ ] Daemon: task cancel/retry/prune API, `/health` and `/metrics`, structured logging, config file, TLS
- [ ] Engine language: `null` literal, assignment, `* / %`, unary minus, lists/maps, functions, `while`; a real `db.query` (today it ignores the SQL) and `db.exec` (a no-op)
- [ ] `.ctx` playground in the PWA (Rust VM compiled to WASM, same bytecode as the daemon)
- [ ] Capability-manifest recipes: scripts declare the natives they use (net/fs/db) and the daemon asks for consent
- [ ] Deterministic replay: record host-call traces from the stdio JSON protocol for fixtures and bug reports
- [ ] QR pairing from the Android/shell app, replacing the query-string token with a short-lived exchange
- [ ] CRDT-synced script/recipe store across devices
- [ ] Daemon-to-daemon LAN sync (mDNS plus WebSocket) with a conflict-inspector UI over CRDT history
- [ ] Event triggers in `.ctx` (file-watch, cron, connectivity change) instead of 5s polling
