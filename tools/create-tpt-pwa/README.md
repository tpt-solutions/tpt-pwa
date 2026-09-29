# create-tpt-pwa

Scaffolds a **self-contained app** from this framework's core: a Vite +
Svelte + TypeScript PWA with the full offline-first degradation layer copied
in and CI prewired. Unlike `create-tpt-companion` (which generates a native
companion package inside this monorepo), this produces a standalone project
you can publish or move anywhere.

```sh
node tools/create-tpt-pwa --name my-app --template notes \
    --description "My offline-first thing"
```

| Flag | Meaning |
| --- | --- |
| `--name` (required) | kebab-case app name |
| `--template` | `notes` (default) · `todo` · `chat` — the entity shape is a note record either way; the template only sets labels |
| `--description` | one-liner for the README + manifest |
| `--dir` | target root (defaults to the current directory) |
| `--dry-run` | print the file plan and exit |
| `--force` | allow an existing target directory |

## What you get

- **The tested degradation layer, copied verbatim** from `pwa/src/lib/`:
  storage negotiation (SQLite-on-OPFS → IndexedDB → memory), tombstoned
  deletes, durable outbox with dead-letters, cortex RPC client with
  auto-reconnect, CRDT mirror with persistence, monotonic outbox ordering.
- `src/App.svelte` + `src/main.ts` wired to it (`initApp`, optimistic
  create/edit/delete), restyled per template.
- The ~2KB service-worker generator (`pnpm build` emits `dist/sw.js`),
  production CI workflow, gitignore, and a README that explains the model.
- Dual-license headers everywhere (`MIT OR Apache-2.0`).

To add native superpowers, run the cortex-daemon next to it (see the
framework README) — the header chip flips to "cortex connected" and the
outbox drains through the daemon's persistent queue.
