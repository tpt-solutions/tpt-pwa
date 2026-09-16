# tpt-pwa (Layer 1: the web app)

Offline-first notes PWA: Svelte 5 + TypeScript + Vite, no app store required. This package is the whole product on its own — the `tpt-cortex` native daemon (Layer 3) is a pure enhancement detected at runtime, never a build-time dependency.

## Development

```sh
pnpm install
pnpm dev        # dev server (no Service Worker, HMR intact)
pnpm check      # svelte-check + tsc
pnpm test       # vitest (sync fallback paths, storage, CRDT)
pnpm build      # production build + generates dist/sw.js (cache-first SW)
pnpm preview    # serve dist/ — test offline mode / SW here
pnpm icons      # regenerate raster icons (PWA + Tauri) from scripts/generate-icons.mjs
```

## Architecture map (src/lib/)

| Module | Role |
| --- | --- |
| `storage/` | Capability-negotiated persistence: SQLite-in-Wasm on OPFS → IndexedDB → in-memory, behind one `NoteStorage` interface |
| `sync.ts` | The spec §4 sync engine: durable outbox + `flush()` with two paths (daemon hand-off or direct HTTP), idempotent and serialized |
| `cortex-client.ts` | JSON-RPC 2.0 over `ws://127.0.0.1:9911`; `checkCortexConnection()` is the single branch point between native and fallback behavior; sets `window.cortexConnected` |
| `crdt.ts` | automerge-rs (Wasm) note-document scaffold for future multi-device merge |
| `stores.ts` / `app.ts` | Svelte stores, optimistic note mutations with rollback, bootstrap |

The Service Worker is **generated**, not hand-maintained: `scripts/generate-sw.mjs` emits a ~2KB cache-first worker from the build manifest after `vite build` (no Workbox — spec §5). Background Sync (`tpt-sync` tag) is relayed to the app via postMessage.

## Graceful degradation in practice

- No daemon → notes still persist (SQLite/OPFS or IndexedDB) and the outbox drains on `online` events, app focus, or Background Sync while a tab is open.
- No OPFS/IndexedDB → session-only in-memory mode; the UI says so in the footer.
- No Wasm/CRDT → sync and storage are unaffected; multi-device merge is simply absent.
