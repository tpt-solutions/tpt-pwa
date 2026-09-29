# Changelog

All notable changes to tpt-pwa are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
[SemVer](https://semver.org/).

## [Unreleased]

### Added
- `cortex-daemon doctor` subcommand: port, origin, auth, queue, data-dir,
  engine and sync-endpoint checks (`--json` for tooling), linked from the
  PWA's cortex status chip.
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
