# Contributing to tpt-pwa

Thanks for your interest in an offline-first, app-store-free web. **This project accepts issues only — pull requests are not accepted** and will be closed without review.

## How to contribute

Open an issue for:

- **Bugs** — what you did, what you expected, what happened. Include the package (`pwa/`, `cortex-daemon/`, `cortex-engine/`, `cortex-shell/`, `cortex-android/`), versions (Node, Go, Rust, browser/OS), and a minimal reproduction or log output.
- **Feature requests and ideas** — describe the problem you're trying to solve before proposing a solution.
- **Questions** — if the docs ([README](README.md), [JSON-RPC contract](docs/jsonrpc-contract.md)) don't answer it, ask.
- **Security problems** — don't post exploit details publicly; open an issue asking for a private contact channel.

Please search existing issues first to avoid duplicates.

## Forking

The code is dual-licensed under **MIT OR Apache-2.0** (SPDX: `MIT OR Apache-2.0`), copyright TPT Solutions, so you're free to fork and modify it under either license. See [LICENSE-MIT](LICENSE-MIT) and [LICENSE-APACHE](LICENSE-APACHE). To start your own companion, see [tools/create-tpt-companion](tools/create-tpt-companion/README.md).

## Design principles (for context when filing issues)

These guide what gets fixed or built, so proposals that fit them are more likely to be acted on:

1. **Capability negotiation, never OS sniffing.** Behaviour branches on what the environment can do, not on user-agent strings.
2. **The PWA must stand alone.** Every feature works with Service Workers + local storage only; the daemon is an enhancement, never a dependency.
3. **First-principles dependencies.** Few runtime dependencies; generated artifacts come from `pwa/scripts/`.
4. **The engine stays total.** `cortex-engine` returns errors, never panics, and never hangs. No `unsafe`.
5. **The contract is strict.** JSON-RPC params reject unknown fields; the contract doc leads.
