# Contributing to tpt-pwa

Thanks for your interest in an offline-first, app-store-free web. **This project accepts issues only — pull requests are not accepted** and will be closed without review.

## How to contribute

Open an issue for:

- **Bugs** — what you did, what you expected, what happened. Include the package (`pwa/`, `cortex-daemon/`, `cortex-engine/`, `cortex-shell/`, `cortex-android/`), versions (Node, Go, Rust, browser/OS), and a minimal reproduction or log output. The [issue templates](.github/ISSUE_TEMPLATE/) collect most of this for you.
- **Feature requests and ideas** — describe the problem you're trying to solve before proposing a solution.
- **Questions** — if the docs ([README](README.md), [JSON-RPC contract](docs/jsonrpc-contract.md), [language reference](docs/language-reference.md), [troubleshooting](docs/troubleshooting.md)) don't answer it, ask.
- **Security problems** — do **not** open an issue; follow [SECURITY.md](SECURITY.md) (private reporting, 5-day acknowledgement).

Please search existing issues first to avoid duplicates.

## Forking

The code is dual-licensed under **MIT OR Apache-2.0** (SPDX: `MIT OR Apache-2.0`), copyright TPT Solutions, so you're free to fork and modify it under either license. See [LICENSE-MIT](LICENSE-MIT) and [LICENSE-APACHE](LICENSE-APACHE).

### Repo layout — where things live and how each is tested

| Path | What it is | Checks |
| --- | --- | --- |
| `pwa/` | Svelte + TS frontend (Vite), service worker generator, storage/sync/CRDT layers | `pnpm --filter tpt-pwa check` (svelte-check + tsc), `pnpm --filter tpt-pwa test` (vitest, `test:coverage` for coverage) |
| `cortex-daemon/` | Go JSON-RPC daemon, persistent queue, scheduler | `go vet ./...`, `go test ./...`; `gofmt` enforced by the pre-commit hook (`gofmt -l` must be empty) |
| `cortex-engine/` | Rust `.ctx` bytecode VM | `cargo fmt --check`, `cargo test`, `cargo clippy -- -D warnings` |
| `cortex-shell/` | Tauri desktop shell (sidecar daemon) | `cargo check` in `src-tauri/` after building the PWA into `dist/` |
| `cortex-android/` | Android companion service + WebView bridge | `./gradlew` lint/test (JVM unit tests pin the contract URL) |
| `tools/create-tpt-companion`, `tools/create-tpt-pwa` | Scaffolders | exercised by their own tests; generated Go/Rust output must compile and test green |
| `scripts/`, `.github/workflows/` | Installers, hooks, version-sync check, CI | `node scripts/check-version-sync.mjs` runs in CI |

CI ([ci.yml](.github/workflows/ci.yml)) is path-filtered: a job only runs when
its package changed. `pnpm test` from the root runs the three main suites
(vitest, go test, cargo test) in one shot.

### Running the stack locally

```sh
pnpm dev                # PWA dev server → http://localhost:5173
pnpm run dev:daemon     # daemon → ws://127.0.0.1:9911/rpc (flags in the README table)
```

- With both running, the app header flips to **cortex connected**. Without the
  daemon, everything still works offline — that's the design (principle 2 below).
- `go run ./cmd/cortex-daemon doctor` (from `cortex-daemon/`) diagnoses port,
  token, queue, engine and sync-endpoint problems; `-json` for tooling.
- [examples/local-sync](examples/local-sync/run.sh) walks the full chain
  (daemon → engine VM → mock sync endpoint) with one command and is the
  fastest way to see the wire protocol live.
- Android and shell debugging notes live in their READMEs; port-collision,
  firewall and WebView gotchas are collected in
  [docs/troubleshooting.md](docs/troubleshooting.md).

### Regression tests first

The convention every fix in this repo followed: **write the failing test or
minimal repro before the fix**, and land them together. Bugs found by reading
code get a regression test that fails on the old code; concurrency and
failure-injection paths get deterministic tests (see `cortex-daemon`
queue-failure-injection and `cortex-engine` adversarial-safety tests for the
pattern). A fix without its test doesn't merge — if you're forking, keep the
same bar.

### Starting your own companion or app

- Native companions (inside this tree or your fork): `node tools/create-tpt-companion --name cortex-foo --lang go|rust|ts` — see [tools/create-tpt-companion/README.md](tools/create-tpt-companion/README.md). Generated packages compile and test green out of the box and speak the [JSON-RPC contract](docs/jsonrpc-contract.md).
- Standalone apps: `pnpm scaffold:app -- --name my-app` — see [tools/create-tpt-pwa/README.md](tools/create-tpt-pwa/README.md).

### Conventions forks should keep

- **License headers**: every source file starts with `Copyright <year> TPT Solutions. Dual-licensed MIT OR Apache-2.0.` — the scaffolders emit them; keep them on new files.
- **Manifest license fields**: `package.json`/`Cargo.toml` carry `"license": "MIT OR Apache-2.0"`; `scripts/check-version-sync.mjs` also enforces one version across all manifests.
- **Contract changes lead with the doc**: if you touch methods or payloads, update [docs/jsonrpc-contract.md](docs/jsonrpc-contract.md) in the same change — three runtimes (PWA, daemon, Android bridge) read it as their spec.

### Style and commits

- Formatting is mechanical and enforced: `gofmt`, `cargo fmt`, Prettier-free but svelte-check-clean TS/Svelte. Don't mix formatting churn into behavioural commits.
- One logical change per commit, imperative subject line ("Make the PR template enforce the issues-only policy", not "fixed some stuff"). The subject should be readable as the changelog entry, because for maintainers it becomes one.
- Every behavioural fix lands with its regression test in the same commit, per the section above.
- Docs are part of the change, not a follow-up: contract → [docs/jsonrpc-contract.md](docs/jsonrpc-contract.md), behaviour → [CHANGELOG.md](CHANGELOG.md) `Unreleased`, task state → [TODO.md](TODO.md).

### Releases (maintainers)

1. Land remaining `Unreleased` [CHANGELOG.md](CHANGELOG.md) entries, then cut a version section with the date (Keep a Changelog format).
2. Bump the version **everywhere at once** — `scripts/check-version-sync.mjs` fails CI if any manifest drifts (root + `pwa/` + `cortex-shell/` `package.json`, `tauri.conf.json`, both `Cargo.toml` files, and the Android build config).
3. Tag `vX.Y.Z` and push. Two workflows fire on the tag: [release.yml](.github/workflows/release.yml) (signed Android APK + companion download page assets) and [release-binaries.yml](.github/workflows/release-binaries.yml) (daemon + engine archives for linux/macos/windows × amd64/arm64, per-target SHA256SUMS, SLSA attestations — verify with `gh attestation verify`).
4. Packaging manifests (winget/scoop/Homebrew in [packaging/](packaging/README.md)) are updated against the new tag; those repos are external, so it's a PR there, not here.

## Design principles (for context when filing issues)

These guide what gets fixed or built, so proposals that fit them are more likely to be acted on:

1. **Capability negotiation, never OS sniffing.** Behaviour branches on what the environment can do, not on user-agent strings.
2. **The PWA must stand alone.** Every feature works with Service Workers + local storage only; the daemon is an enhancement, never a dependency.
3. **First-principles dependencies.** Few runtime dependencies; generated artifacts come from `pwa/scripts/`.
4. **The engine stays total.** `cortex-engine` returns errors, never panics, and never hangs. No `unsafe`. Parse/compile failures are *permanent* (exit 2); runtime failures are *transient* (exit 1) — the scheduler leans on this.
5. **The contract is strict.** JSON-RPC params reject unknown fields; the contract doc leads.
6. **The daemon is the only component with OS access.** Scripts, tabs, and the WebView are untrusted; see [SECURITY.md](SECURITY.md) for the invariants that follow.
7. **Deletes survive.** Tombstones, idempotency keys, and dead-letters over silent drops — sync state must be inspectable.
