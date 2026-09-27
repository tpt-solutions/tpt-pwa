# Contributing to tpt-pwa

Thanks for helping build an offline-first, app-store-free web. A few things to know before you open a PR.

## Licensing (important)

The project is dual-licensed under **MIT OR Apache-2.0** (SPDX: `MIT OR Apache-2.0`), copyright TPT Solutions. By contributing, you agree that your contributions are licensed under both licenses, at the option of the downstream users — the same terms as the rest of the project. No CLA is required; your git commit serves as the license declaration.

Every package carries the license in its manifest:

- `package.json`: `"license": "MIT OR Apache-2.0"`
- `Cargo.toml`: `license = "MIT OR Apache-2.0"`
- `go.mod`-based packages: the repo-level licensing applies; keep source file headers (`// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.`) on new files.

## Development

| Package | Stack | Commands |
| --- | --- | --- |
| `pwa/` | Svelte 5 + TS + Vite | `pnpm install && pnpm check && pnpm test && pnpm build` |
| `cortex-daemon/` | Go | `go vet ./... && go build ./... && go test ./...` |
| `cortex-engine/` | Rust | `cargo fmt --check && cargo clippy -- -D warnings && cargo test` |
| `cortex-shell/` | Tauri 2 | `pnpm install && cargo check` (in `src-tauri/`) |
| `cortex-android/` | Kotlin + Gradle 8.9 | `./gradlew assembleDebug` (JDK 17) |

CI runs these per-language, path-filtered (`.github/workflows/ci.yml`). If you touch the wire contract ([docs/jsonrpc-contract.md](docs/jsonrpc-contract.md)), update the PWA client (`pwa/src/lib/cortex-client.ts`), the Go daemon, and the doc in the same PR.

Run `pnpm run hooks:setup` once to enable the pre-commit hook (`scripts/githooks/pre-commit`), a fast subset of CI: svelte-check + vitest for the PWA, gofmt/vet for the daemon, and `cargo fmt --check` for the engine.

## Ground rules

1. **Capability negotiation, never OS sniffing.** Branch on what the environment can do (`checkCortexConnection()`, storage negotiation), not on user-agent strings (spec §2/§4).
2. **The PWA must stand alone.** Every feature has to work with Service Workers + local storage only; the daemon is an enhancement, never a dependency.
3. **First-principles dependencies.** New runtime deps need a justification in the PR description; generated artifacts (SW, icons) come from `pwa/scripts/`, not from heavyweight tooling.
4. **The engine stays total.** Changes to `cortex-engine` must keep the safety suite (`tests/safety.rs`) green: errors, never panics; budget, never hangs. No `unsafe`.
5. **The contract is strict.** JSON-RPC params reject unknown fields — if you add a field, it goes in the contract doc first.
