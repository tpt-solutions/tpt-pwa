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

### Getting a fork green

The repo is a pnpm workspace with three toolchains (Node 22/pnpm 11, Go 1.25, Rust 1.85 — `.tool-versions`/`mise.toml` pin them; the devcontainer sets them up for you). From the root:

```sh
pnpm install && pnpm run hooks:setup   # deps + pre-commit hooks mirroring CI
pnpm test                              # vitest + go test + cargo test
```

Per-area loops live in each package (`pnpm run check` / `go vet ./...` /
`cargo clippy -- -D warnings`). CI mirrors exactly these plus the
`cortex-shell` and `cortex-android` jobs.

### Starting your own companion or app

- Native companions (inside this tree or your fork): `node tools/create-tpt-companion --name cortex-foo --lang go|rust|ts` — see [tools/create-tpt-companion/README.md](tools/create-tpt-companion/README.md). Generated packages compile and test green out of the box and speak the [JSON-RPC contract](docs/jsonrpc-contract.md).
- Standalone apps: `pnpm scaffold:app -- --name my-app` — see [tools/create-tpt-pwa/README.md](tools/create-tpt-pwa/README.md).

### Conventions forks should keep

- **License headers**: every source file starts with `Copyright <year> TPT Solutions. Dual-licensed MIT OR Apache-2.0.` — the scaffolders emit them; keep them on new files.
- **Manifest license fields**: `package.json`/`Cargo.toml` carry `"license": "MIT OR Apache-2.0"`; `scripts/check-version-sync.mjs` also enforces one version across all manifests.
- **Contract changes lead with the doc**: if you touch methods or payloads, update [docs/jsonrpc-contract.md](docs/jsonrpc-contract.md) in the same change — three runtimes (PWA, daemon, Android bridge) read it as their spec.

## Design principles (for context when filing issues)

These guide what gets fixed or built, so proposals that fit them are more likely to be acted on:

1. **Capability negotiation, never OS sniffing.** Behaviour branches on what the environment can do, not on user-agent strings.
2. **The PWA must stand alone.** Every feature works with Service Workers + local storage only; the daemon is an enhancement, never a dependency.
3. **First-principles dependencies.** Few runtime dependencies; generated artifacts come from `pwa/scripts/`.
4. **The engine stays total.** `cortex-engine` returns errors, never panics, and never hangs. No `unsafe`. Parse/compile failures are *permanent* (exit 2); runtime failures are *transient* (exit 1) — the scheduler leans on this.
5. **The contract is strict.** JSON-RPC params reject unknown fields; the contract doc leads.
6. **The daemon is the only component with OS access.** Scripts, tabs, and the WebView are untrusted; see [SECURITY.md](SECURITY.md) for the invariants that follow.
7. **Deletes survive.** Tombstones, idempotency keys, and dead-letters over silent drops — sync state must be inspectable.
