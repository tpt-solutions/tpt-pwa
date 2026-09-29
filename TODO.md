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

Findings from a second full review (PWA, daemon, engine, adoption). Found by reading code, not by running it: write a failing regression test or repro first, then fix. Tier 1 = bugs, Tier 2 = adoption, Tier 3 = features/ideas. Worked through 2026-09-30; every fix landed with its regression test and a green suite.

### Tier 1 — CI/tooling bugs

- [x] Fix CI PR path filters — `ci.yml` used `pull_request.changed_files` (an integer count), so PR jobs never ran; replaced with a `changes` job using `dorny/paths-filter`, and the PWA job now runs `pnpm run test`
- [x] Verify the CI fix with a throwaway PR touching only `pwa/` (pwa job runs, others skip) — DONE: PR #1 (`ci/pwa-probe`, closed as throwaway) ran `Detect changed areas` + `PWA` and skipped everything else. The shakeout also surfaced and fixed five real CI bugs on master: missing pnpm version pin (`packageManager` field), workspace-wide installs triggering prepare-sidecar in the PWA job, generate-sw running main() on import without a dist/, `gradlew` missing its exec bit, and the shell job's cargo check running in the wrong directory without a PWA build. Master CI is fully green across the whole matrix (daemon ubuntu+windows, engine ubuntu+windows+macos, shell, android)
- [x] Make `cortex-shell/scripts/prepare-sidecar.mjs` fail loudly instead of writing a 0-byte sidecar; have CI run it — exits non-zero with build instructions (escape hatch: `CORTEX_SIDECAR_ALLOW_PLACEHOLDER=1`); the CI shell job now builds a real sidecar before `pnpm install` (which runs the script)
- [x] Sign the release APK and build the gomobile `.aar` in `release.yml` — gomobile bind step (NDK auto-detected), env-driven signing config wired in `app/build.gradle.kts` via `APK_KEYSTORE_BASE64`/`APK_KEYSTORE_PASSWORD`/`APK_KEY_ALIAS`/`APK_KEY_PASSWORD` secrets (unsigned fallback preserved), fixed artifact names, idempotent release create/upload
- [x] Check `cortex-android/app/proguard-rules.pro` keeps the reflectively loaded `Mobile` class — it kept `cortex.**` but gomobile generates `solutions.tpt.cortex.Mobile`; rules now keep that class plus gomobile's `go.**` JNI runtime
- [x] Verify the root `dev:daemon` script — it was broken (root has no go.mod; repro'd); now `cd cortex-daemon && go run ./cmd/cortex-daemon`, verified the daemon starts
- [x] Add `.gitattributes` (`* text=auto`; `gradlew` and `*.sh` as LF; binary markers for wasm/png/aar/keystore)
- [x] Scaffold CLI fixes — Go test template no longer shadows `status()` (`got := status()`), unparseable `pnpm-workspace.yaml` lists fail loudly instead of silently skipping registration (regex also tolerates unquoted entries), TS template carries typescript/vitest devDeps, new `--dry-run`/`--force`; generated Go and Rust output re-verified compile+test green

### Tier 1 — PWA bugs

- [x] Persist and load the CRDT — new `crdt-store.ts` (dedicated IndexedDB, independent of the negotiated note storage): snapshot loaded on open, saved after every mutation, seeded from `listNotes()` on first run
- [x] Flush debounced edits on `pagehide`/`visibilitychange` (`flushPendingSaves`, awaitable for tests); `deleteNote` clears the pending save timer so a debounce can't resurrect a deleted note
- [x] Fix rollback — `createNote` undoes the storage write when a later step fails and always removes the optimistic insert; tests cover both failure points
- [x] Make sync hand-off idempotent — outbox ordering is monotonic (stall/jump-proof), daemon hand-offs carry a content-derived `batchId` the queue dedupes (`EnqueueIdempotent` + `deduplicated` in the result), dequeues are sequential, and the transport verifies the daemon's `accepted` count
- [x] Add tombstones for deletes — `Note.deletedAt` through all three backends (SQLite gets an in-place migration), outbox delete payloads carry `deletedAt`, and the CRDT tombstones instead of removing so a concurrent edit can't resurrect a deletion (merge test added). Found and fixed on the way: `automerge-wasm`'s `put(…, null)` corrupts later saves — absence of the key now encodes "live"
- [x] Make `#markSynced` one atomic storage op — `markSyncedNote(id, syncedAt, maxUpdatedAt)`: single conditional `UPDATE` in SQLite, conditional write inside one transaction in IndexedDB
- [x] Storage backend switching — the chosen backend persists in `localStorage` and is pinned first on the next boot; a switch migrates notes/outbox/dead-letters (`migrateStorage`); Web Locks leader election (`tpt-pwa-storage-leader`) lets ONE tab negotiate/migrate while followers open the same database without flipping the choice
- [x] `CortexRPC` — in-flight `connect()` memoized, auto-reconnect with capped backoff after the first real handshake, pending calls rejected on socket close, `enqueueSyncTask` validates the ack (`accepted` count / missing result → degrade)
- [x] Service worker — `/sw.js` excluded from the fetch handler, revision hash covers file contents, install no longer `skipWaiting()`s: it posts `tpt-sw-update`, the app shows an "Update ready" chip, and the user applies it (`tpt-sw-skip-waiting` → `controllerchange` → reload)
- [x] HTTP sync path — permanent 4xx (except 408/429) dead-letters the entry (`dead_letters` table/store + inspection API); the placeholder `https://api.tpt/sync` default is gone: with no endpoint configured the HTTP path is disabled and entries wait for the daemon
- [x] Smaller PWA fixes — `newNote`/`removeCurrent` no longer produce unhandled rejections, `appinstalled` clears the install prompt, a failed automerge load no longer memoizes `null` permanently (retryable), `crdt.merge` frees the peer doc on every path (try/finally), `updateNote` revisions are monotonic under backwards clock jumps
- [x] PWA test gaps — new `app.test.ts` (create/rollback/coalescing/pagehide/tombstone/clock), `cortex-client.test.ts` (memoized connect, pending rejection, backoff reconnect, transport ack validation), storage `markSyncedNote`/dead-letter tests, `generate-sw.test.ts` (content hashing, sw.js exclusion, update flow), CRDT tombstone merge roundtrip (multi-tab coordination is covered by the Web Locks leader design rather than a simulated second tab)

### Tier 3 quick wins (pulled from the menu below)

- [x] Register Background Sync (`tpt-sync` was never registered, so the SW handler was dead code) — `background-sync.ts` (`requestBackgroundSync`), armed after every flush that leaves entries queued; no-op where unsupported
- [x] `navigator.storage.persist()` requested at bootstrap (durability hint; denial logged in dev)
- [x] Daemon: task `cortex.task.cancel` / `cortex.task.retry` / `cortex.task.prune` API (queue methods + contract doc; lifecycle tested), plus `GET /health` and `GET /metrics` (Prometheus text) on the same loopback server

### Tier 1 — Daemon (Go) bugs

- [x] `OnTransition` reports the resulting state (already fixed in c5553d3 — verified `failed` broadcasts flow)
- [x] Signal handling — `signal.NotifyContext` for SIGINT/SIGTERM in `main.go` (also landed earlier; verified)
- [x] `engineexec.go` — kill the engine process BEFORE `Wait` on decode errors and write failures (a wedged engine can no longer deadlock the executor)
- [x] Per-task timeout — the scheduler's `TaskTimeout` is actually applied now (each execution runs under its own deadline; timeouts requeue); `syncexec` and `engineexec` default to 30s-bounded HTTP clients
- [x] WebSocket read limit — sized above the base64 of a 4 MiB payload (landed earlier; exercised by tests)
- [x] `fs.write` — symlinks/junctions resolved on root and destination parent before the containment check (test proves a symlink escape is rejected); writes go temp file + rename (atomic, no truncated files)
- [x] `rpc.Broadcast` — per-connection write mutexes, snapshot-then-write outside the broker lock, and a keepalive pinger that drops clients that stop answering
- [x] `queue.go` — fsync before rename, finished tasks pruned beyond a 100-task inspection cap (pending work never pruned), corrupt files moved aside (`queue.json.corrupt-<ts>`) instead of refusing to boot, in-memory append/mutation rolled back when a save fails (cross-platform failure-injection tests)
- [x] Graceful shutdown — `Run` waits for the HTTP server's shutdown AND the scheduler goroutine before returning (gomobile embedders rely on it)
- [x] Enqueue validation — unknown kinds rejected with `-32602` (`syncNotes`, `crdtMerge` allow-list), exactly one of `entries`/`payload` enforced, `batchId` idempotency keys dedupe retried hand-offs, permanent failures (`queue.PermanentError`, e.g. sync 4xx) park immediately instead of burning retries, and pushes carry the entry id for server-side dedup
- [x] Origin pattern + mobile auth — the dead `/8` pattern is gone (already clean); `mobile.Start` now REQUIRES a token: DaemonService generates one per boot, passes it to `Mobile.start` (4-arg) and the bridge URL, relays it into the WebView as a `cortex:auth` DOM event, and the PWA appends it to every connect (contract doc updated)

### Tier 1 — Engine (Rust) bugs

- [x] Depth limit and token cap — `MAX_NESTING_DEPTH` (128) counts parser recursion AND left-associative chain iterations (the subtle one: `1+1+1…` built unbounded AST depth through a loop), `MAX_TOKENS` (100k) caps the parse; adversarial tests for parens/blocks/`!`-chains/else-if chains/token floods, plus a proof that depth-100 still parses. This also bounds compiler recursion and the AST's recursive `Drop` (deep programs can no longer overflow the stack at all)
- [x] Jump targets — `Compiler::offset` uses `u16::try_from`; a 66k-instruction program with a trailing `if` is a compile error instead of silent wraparound
- [x] Quadratic `LoadLocal` list cloning — `Value::List`/`Map` are `Rc`-backed (the DSL is immutable), so loop iterators clone a refcount, not a vector
- [x] Distinct exit codes — parse/compile failures exit 2 (permanent), runtime failures exit 1 (transient); integration-tested; the daemon's probe semantics unchanged
- [x] Short-circuit `&&`/`||` (new `JumpIfTrue`, `Not;Not` bool coercion — a native call on the right side provably doesn't fire); NaN ordering is a `TypeMismatch` data error instead of silently "less"; mixed int/float comparisons are exact (no silent f64 rounding of 2^53-scale ints); list index and integer overflow are `IndexOutOfRange`/`Overflow` data variants, `BadProgram` reserved for structural faults

### Tier 2 — Adoption

- [x] Release binaries — `release-binaries.yml`: daemon + engine for linux/macos/windows × amd64/arm64, per-target `SHA256SUMS-<target>.txt`, `--generate-notes`, and a tests-first gate
- [x] One-line installers — `scripts/install.sh` + `scripts/install.ps1` (checksum-verified, PATH guidance); winget/scoop/Homebrew manifests in `packaging/` (publish flow documented there — bucket/tap repos and the winget-pkgs PR are external)
- [x] `cortex doctor` — `cortex-daemon doctor` subcommand (port, origin, auth token, queue health, data-dir writability, engine spawn, sync endpoint reachability, version; `--json` for tooling), documented in the README quickstart
- [x] Devcontainer plus `mise.toml`/`.tool-versions` (Node 22, pnpm 11, Go 1.25, Rust 1.85); Dockerfile for the daemon (distroless, flag-driven, token-first docs)
- [x] Examples runnable with one command — `examples/local-sync/run.sh` / `run.ps1` (build → mock endpoint → engine-backed daemon → enqueue → teardown, verified end-to-end on Windows); no hardcoded `.exe` paths anywhere
- [x] `create-tpt-pwa` starter — `tools/create-tpt-pwa` (`pnpm scaffold:app -- --name my-app --template notes|todo|chat`): self-contained Vite+Svelte+TS app with the tested degradation layer copied verbatim, SW generator, and CI prewired
- [x] Register scaffolded Go/Rust companions in CI — convention: `pnpm scaffold -- --dir companions`; dynamic-matrix `discover-companions` → `companion-go`/`companion-rust` jobs vet+test each
- [x] `.ctx` recipe cookbook — `examples/recipes/` (retry-upload, periodic-fetch, batch-sync), each exercised by `cortex-engine/tests/recipes.rs` (parse+compile+run, connectivity-gating asserted)
- [x] `SECURITY.md` — trust boundaries, loopback/token rules, `fs` sandbox, engine budget, reporting process
- [x] Architecture diagram (Mermaid) in the README
- [x] `.ctx` language reference — [docs/language-reference.md](docs/language-reference.md)
- [x] Troubleshooting/FAQ — [docs/troubleshooting.md](docs/troubleshooting.md) (port 9911, Android cleartext-loopback WebView, Windows firewall, corrupt queues, dead-letters)
- [x] CHANGELOG, `CODE_OF_CONDUCT.md`, issue/PR templates, `CODEOWNERS`; expand `cortex-shell/README.md` and `docs/jsonrpc-contract.md` (batchId/dedup, accepted count, token, push envelope, sandbox notes). `CONTRIBUTING.md` expansion is still open
- [x] CI: Dependabot, CodeQL (JS+Go), `govulncheck`, `cargo audit`, `pnpm audit`, version-sync check (`scripts/check-version-sync.mjs`); Windows/macOS CI matrix (daemon on win, engine on all three); coverage reporting (vitest v8 coverage in CI, `go test -cover`); SLSA provenance attestations on release binaries (`gh attestation verify`). Still open: Gradle cache tuning beyond setup-gradle defaults

### Tier 3 — Features and ideas (menu, unprioritized)

- [ ] Product gaps: search, export/import (JSON/Markdown), `navigator.storage.persist()` (DONE), encryption at rest and in sync, multi-tab coordination, register Background Sync (DONE), a11y (live regions, aria-labels, reduced motion), i18n
- [x] Daemon: task cancel/retry/prune API, `/health` and `/metrics` — DONE (see Tier 3 quick wins). Still open: structured logging, config file, TLS
- [ ] Engine language: `null` literal, assignment, `* / %`, unary minus, lists/maps, functions, `while`; a real `db.query` (today it ignores the SQL) and `db.exec` (a no-op)
- [ ] `.ctx` playground in the PWA (Rust VM compiled to WASM, same bytecode as the daemon)
- [ ] Capability-manifest recipes: scripts declare the natives they use (net/fs/db) and the daemon asks for consent
- [ ] Deterministic replay: record host-call traces from the stdio JSON protocol for fixtures and bug reports
- [ ] QR pairing from the Android/shell app, replacing the query-string token with a short-lived exchange
- [ ] CRDT-synced script/recipe store across devices
- [ ] Daemon-to-daemon LAN sync (mDNS plus WebSocket) with a conflict-inspector UI over CRDT history
- [ ] Event triggers in `.ctx` (file-watch, cron, connectivity change) instead of 5s polling
