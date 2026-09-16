# tpt-pwa

An offline-first, app-store-free web application framework. tpt-pwa pairs a lightweight Svelte/TypeScript frontend with the optional `tpt-cortex` native companion to deliver true background processing, persistent local storage, and hardware access — while gracefully degrading to standard Web APIs (Service Workers + IndexedDB) when the native companion isn't available. See [spec.txt](spec.txt) for the full design document.

## Monorepo layout

```
tpt-pwa/
├── pwa/                 # Layer 1: Svelte + TS frontend (Vite, plain Svelte)
├── cortex-daemon/       # Layer 3: Go IPC host / WebSocket server / scheduler
├── cortex-engine/       # Layer 3: Rust DSL VM, bytecode executor
├── cortex-shell/        # Tauri-based desktop installer bundling the daemon
├── cortex-android/      # Android companion (runs cortex-daemon as a service)
├── docs/                # Design docs, ADRs
├── LICENSE-MIT
├── LICENSE-APACHE
└── TODO.md              # Phased task checklist
```

Progress is tracked in [TODO.md](TODO.md). Contributing? Start with [CONTRIBUTING.md](CONTRIBUTING.md) (dual-license terms) and the shared [JSON-RPC contract](docs/jsonrpc-contract.md). Formal-verification scope for the engine lives in [docs/formal-verification.md](docs/formal-verification.md).

## License

Licensed under either of

- MIT license ([LICENSE-MIT](LICENSE-MIT))
- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))

at your option. SPDX identifier: `MIT OR Apache-2.0`.

Copyright (c) 2026 TPT Solutions.
