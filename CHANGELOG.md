# Changelog

All notable changes to tpt-pwa are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
[SemVer](https://semver.org/).

## [Unreleased]

### Added
- Recurring tasks: `cortex.task.enqueue` accepts `every` (e.g. `"5m"`) and
  the queue requeues each successful completion with a fresh attempt budget.
  Recurrence survives crashes (a completed recurring task resumes on
  restart), is immune to pruning, and stops on cancel or permanent failure —
  periodic recipes now need exactly one enqueue (docs/jsonrpc-contract.md).
- Capability manifests: `cortex-engine manifest` prints a script's static
  native-call surface as JSON, and the daemon's `-allow-natives` enforces it
  before any task executes — scripts exceeding the allowlist park as
  permanently failed without a single native call firing (also settable via
  the config file's `allow-natives` key).
- Deterministic replay: `cortex-engine exec-host --record` (and `run
  --record`) write JSONL traces of every native call plus the final
  outcome; `cortex-engine replay` re-runs a script against a trace with no
  host attached and names any divergence. A trace captured against a real
  daemon becomes a permanent regression fixture (docs/formal-verification.md
  §"Deterministic replay").
- Engine language upgrade: `null` literals, `*` `/` `%` (checked integer
  division — zero divisors and i64::MIN / -1 are data errors), unary minus,
  re-assignment of locals (`x = e;`), `while` loops, list and map literals
  (`[1, 2]`, `{"k": v}`, indexable interchangeably with host data), and
  task-level `fn` definitions with compile-time arity checks, mutual
  recursion, and a 128-frame call-depth cap. [docs/language-reference.md](docs/language-reference.md)
  rewritten to match; adversarial bytecode fuzzing extended to the new
  instructions (frames, BuildList/BuildMap, CallFn).
- PWA note search: multi-term AND matching over title and body with
  title-over-body ranking, wired to an accessible search box with a live
  result count.
- PWA export/import: JSON (lossless round-trip) and Markdown (human-readable,
  metadata carried in invisible `<!-- tpt-pwa-note -->` markers; foreign
  Markdown imports as a single note). Imports merge last-writer-wins through
  the normal outbox, so they propagate to other devices.
- PWA accessibility: `aria-live` announcements for search results, import
  summaries and offline/queued state; labels on the previously
  placeholder-only editor fields; `prefers-reduced-motion` disables view
  transitions and animations (both the CSS and the `startViewTransition`
  path).
- `cortex-daemon doctor` subcommand: port, origin, auth, queue, data-dir,
  engine and sync-endpoint checks (`--json` for tooling), linked from the
  PWA's cortex status chip.
- Daemon structured logging: `-log-format text|json` and `-log-level`;
  library logs go through slog with `component` attrs (json emits one
  parseable object per line).
- Daemon `-config` JSON file: keys mirror the flags, explicit flags override,
  unknown keys and bad values fail loudly naming the file; `doctor` honors it
  too.
- Release binaries workflow: daemon + engine archives for
  linux/macos/windows on amd64/arm64 with per-target SHA256SUMS and a
  tests-first gate; one-line installers (`scripts/install.sh`,
  `scripts/install.ps1`).
- `.ctx` language reference, troubleshooting guide, and a recipe cookbook
  (`docs/`).
- `.gitattributes` (LF for scripts, binary markers), devcontainer, `mise.toml`,
  and a Dockerfile for the daemon.
- CI: CodeQL, Dependabot, `govulncheck`, `cargo audit`, `pnpm audit`, and a
  manifest version-sync check.
- The PR template redirects contributors to the issue tracker: this repo
  accepts issues only.
- CI shakeout (found by the first real workflow runs): pnpm pinned via
  `packageManager`, workspace-scoped installs, direct-execution guard for
  the SW generator, `gradlew` exec bit, shell job builds the PWA and runs
  cargo check in `src-tauri/`.
- CONTRIBUTING.md expansion: repo layout/test map, local dev loop,
  regression-test-first convention, style and commit conventions, and the
  maintainer release process.

### Changed
- Android CI enables the Gradle build cache and parallel execution so
  setup-gradle's persisted caches reuse task outputs (configuration cache
  deferred until CI-verified).

### Changed
- Service-worker updates no longer `skipWaiting()` mid-session: a waiting
  worker announces itself and the user applies it from the "Update ready"
  chip. The revision hash now covers file contents, and `/sw.js` is excluded
  from the fetch handler.
- Sync hand-off is idempotent (content-derived `batchId`, daemon-side
  dedup, `accepted` count verification) and outbox ordering is monotonic
  under clock stalls. HTTP-path permanent 4xx entries dead-letter; the
  placeholder `https://api.tpt/sync` default is gone (HTTP fallback is
  disabled until an endpoint is configured).
- Deletes are tombstones (`Note.deletedAt`) in storage, sync, and the CRDT,
  so a delete can no longer lose to a concurrent edit.
- `markSynced` is one conditional storage operation; the RPC client
  auto-reconnects with backoff and rejects pending calls on drop; storage
  choice persists, migrates, and second tabs follow the Web Locks leader.
- Engine: `&&`/`||` short-circuit; NaN comparisons are data errors; mixed
  int/float comparisons are exact (no silent f64 rounding of large i64s);
  list/overflow errors are data variants; exit code 2 means permanent
  (parse/compile) failure; parser depth/token caps; compiler rejects jump
  targets past u16; loop iterator cloning is O(1) (Rc-backed lists).
- Daemon: per-task execution timeout; permanent failures (`queue.PermanentError`)
  park immediately; unknown task kinds rejected at enqueue with
  `exactly-one-of entries/payload`; queue file is fsynced, pruned, and
  corrupt-file tolerant with in-memory rollback on failed saves; `fs.write`
  resolves symlinks and writes atomically; broadcasts and keepalive pings
  never hold the broker lock; graceful shutdown waits for the scheduler.

### Fixed
- Android release APK signing + gomobile `.aar` build in the release
  workflow; proguard keeps the reflectively loaded `Mobile` class.
- `prepare-sidecar.mjs` fails loudly instead of writing a 0-byte sidecar; CI
  builds a real sidecar binary.
- Root `dev:daemon` script actually runs (go.mod lives in `cortex-daemon/`).
- Scaffold CLI: no shadowed `status()` in the Go test template, loud failure
  when the workspace list can't be parsed, TS template carries
  typescript/vitest devDeps, new `--dry-run`/`--force` flags.

## [0.1.0] - 2026-09-30

Initial public state: PWA core (offline-first notes, SQLite/IndexedDB/in-memory
negotiation, custom minimal service worker, CRDT mirror, telemetry panel),
cortex-daemon (JSON-RPC 2.0 over loopback WebSocket, persistent task queue,
scheduler, engine-backed execution), cortex-engine (`.ctx` bytecode VM with
native bindings and adversarial safety tests), Android companion (foreground
service + JSON-RPC bridge + direct-APK distribution), Tauri desktop shell,
and the `create-tpt-companion` scaffolder.
