#!/usr/bin/env node
// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
// create-tpt-pwa: scaffold a NEW app from this framework's core — a Vite +
// Svelte + TS PWA with the offline-first degradation layer (storage
// negotiation, cortex RPC client, sync manager, CRDT mirror) and CI, all
// prewired. Run from the repo root:
//
//   node tools/create-tpt-pwa --name my-notes-app [--dir <target>] [--template notes|todo|chat] [--dry-run] [--force]
//
// The generated app is SELF-CONTAINED: the lib modules are copied in (not
// imported from the framework) so it can be published or moved anywhere.
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = fileURLToPath(new URL('.', import.meta.url))
const frameworkPwa = resolve(here, '..', '..', 'pwa')

function parseArgs(argv) {
  const args = { dir: process.cwd(), template: 'notes' }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (flag === '--name') args.name = argv[++i]
    else if (flag === '--dir') args.dir = resolve(argv[++i])
    else if (flag === '--template') args.template = argv[++i]
    else if (flag === '--description') args.description = argv[++i]
    else if (flag === '--dry-run') args.dryRun = true
    else if (flag === '--force') args.force = true
    else if (flag === '--help' || flag === '-h') args.help = true
    else {
      console.error(`create-tpt-pwa: unknown argument ${flag}`)
      process.exit(2)
    }
  }
  return args
}

function fail(message) {
  console.error(`create-tpt-pwa: ${message}`)
  process.exit(2)
}

const LIB_MODULES = [
  'src/lib/app.ts',
  'src/lib/cortex-client.ts',
  'src/lib/crdt.ts',
  'src/lib/crdt-store.ts',
  'src/lib/devlog.ts',
  'src/lib/stores.ts',
  'src/lib/sync.ts',
  'src/lib/telemetry.ts',
  'src/lib/storage/index.ts',
  'src/lib/storage/types.ts',
  'src/lib/storage/memory.ts',
  'src/lib/storage/idb.ts',
  'src/lib/storage/sqlite.ts',
]

const TEMPLATES = {
  notes: { entity: 'note', title: 'Notes', empty: 'Create your first note' },
  todo: { entity: 'note', title: 'Todos', empty: 'Add your first todo' },
  chat: { entity: 'note', title: 'Chat', empty: 'Say something' },
}

function appSvelte(template) {
  const t = TEMPLATES[template] ?? TEMPLATES.notes
  return `<script lang="ts">
  // Copyright ${new Date().getFullYear()} TPT Solutions. Dual-licensed MIT OR Apache-2.0.
  import { onMount } from 'svelte'
  import { appStatus, capabilities, notes, pendingSync } from './lib/stores'
  import { createNote, deleteNote, initApp, updateNote } from './lib/app'

  // App bootstrap: storage negotiation, daemon feature-detection, sync
  // triggers (spec §4). Everything below degrades gracefully without cortex.
  onMount(() => {
    void initApp()
  })

  async function add${t.entity.charAt(0).toUpperCase() + t.entity.slice(1)}() {
    try {
      await createNote('${t.title} ' + new Date().toLocaleTimeString(), '')
    } catch (error) {
      console.warn('create failed', error) // UI already rolled back
    }
  }
</script>

<div class="app">
  <header class="app-header">
    <h1>${t.title}</h1>
    <div class="header-status">
      {#if $capabilities.cortex}
        <span class="chip chip--ok">cortex connected</span>
      {:else}
        <span class="chip">offline-capable mode</span>
      {/if}
      {#if $pendingSync > 0}
        <span class="chip">{$pendingSync} queued</span>
      {/if}
    </div>
  </header>

  <main class="app-main">
    {#if $appStatus === 'loading'}
      <p class="muted">Negotiating capabilities…</p>
    {:else if $appStatus === 'error'}
      <section class="card">
        <h2>Storage unavailable</h2>
        <p class="muted">Reload to retry capability negotiation.</p>
      </section>
    {:else}
      <div class="toolbar">
        <button onclick={() => void add${t.entity.charAt(0).toUpperCase() + t.entity.slice(1)}()}>+ New</button>
      </div>
      {#if $notes.length === 0}
        <p class="muted">${t.empty} — it persists offline.</p>
      {:else}
        <ul>
          {#each $notes as item (item.id)}
            <li>
              <input
                value={item.title}
                oninput={(event) => updateNote(item.id, { title: event.currentTarget.value })}
              />
              <button onclick={() => void deleteNote(item.id)}>delete</button>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </main>

  <footer class="muted">
    storage: {$capabilities.storageBackend ?? 'negotiating'} ·
    crdt: {$capabilities.crdt ? 'ready' : 'unavailable'} ·
    offline-first
  </footer>
</div>

<style>
  .app { max-width: 640px; margin: 0 auto; padding: 1rem; font-family: system-ui, sans-serif; }
  .app-header { display: flex; justify-content: space-between; align-items: center; }
  .chip { font-size: 0.75rem; padding: 0.15rem 0.5rem; border-radius: 999px; background: #eee; }
  .chip--ok { background: #d6f5d6; }
  .card { border: 1px solid #ddd; border-radius: 8px; padding: 1rem; }
  .muted { color: #666; }
  li { display: flex; gap: 0.5rem; margin: 0.25rem 0; }
  li input { flex: 1; }
</style>
`
}

function packageJson(name, description) {
  return JSON.stringify(
    {
      name,
      private: true,
      version: '0.1.0',
      type: 'module',
      license: 'MIT OR Apache-2.0',
      description: description || `${name}: an offline-first tpt-pwa app`,
      scripts: {
        dev: 'vite',
        build: 'vite build && node scripts/generate-sw.mjs',
        preview: 'vite preview',
        check: 'svelte-check --tsconfig ./tsconfig.app.json && tsc -p tsconfig.node.json',
        test: 'vitest run',
      },
      dependencies: {
        '@automerge/automerge-wasm': '1.0.0-preview.0',
        'wa-sqlite': '1.0.0',
      },
      devDependencies: {
        '@sveltejs/vite-plugin-svelte': '^7.3.0',
        '@tsconfig/svelte': '^5.0.8',
        '@types/node': '^24.13.3',
        'svelte': '^5.57.0',
        'svelte-check': '^4.7.6',
        'typescript': '~6.0.2',
        'vite': '^8.3.0',
        'vitest': '^5.0.1',
      },
    },
    null,
    2,
  ) + '\n'
}

function ciWorkflow(name) {
  return `# Offline-first build gate for ${name}: typecheck, unit tests, production
# build (which emits the ~2KB service worker).
name: CI

on:
  push:
    branches: [master]
  pull_request:

jobs:
  pwa:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
      - run: pnpm install --frozen-lockfile
      - run: pnpm run check
      - run: pnpm run test
      - run: pnpm run build
`
}

function readme(name, description, template) {
  return `# ${name}

${description || 'An offline-first app built on the tpt-pwa framework.'}

Offline-first from line one: notes persist through storage negotiation
(SQLite-on-OPFS → IndexedDB → memory), the app shell precaches via a
hand-rolled ~2KB service worker, and edits queue in a durable outbox that
syncs when a path exists — the daemon's persistent queue when the
tpt-cortex companion is running, direct HTTP otherwise.

## Develop

\`\`\`sh
pnpm install
pnpm dev          # vite dev server
pnpm test         # vitest
pnpm build        # production build + service worker
\`\`\`

With a cortex daemon running on the loopback (\`ws://127.0.0.1:9911/rpc\`)
the header chip flips to **cortex connected** and the outbox drains through
its persistent queue — even with every tab closed. See the
[tpt-pwa framework docs](https://github.com/tpt-solutions/tpt-pwa) for the
JSON-RPC contract and the \`.ctx\` task language.

Template: **${template}**. The entity is a plain note record
(\`{id, title, body, createdAt, updatedAt, syncedAt, deletedAt}\`); reshape it
in \`src/lib/storage/types.ts\` and the CRDT mirror in \`src/lib/crdt.ts\`.

## License

Dual-licensed MIT OR Apache-2.0 — see LICENSE-MIT / LICENSE-APACHE.
`
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  if (args.help) {
    console.log('usage: node tools/create-tpt-pwa --name <name> [--template notes|todo|chat] [--dir <target>] [--dry-run] [--force]')
    process.exit(0)
  }
  if (!args.name || !/^[a-z][a-z0-9-]*$/.test(args.name)) {
    fail('--name is required and must be kebab-case')
  }
  if (!TEMPLATES[args.template]) {
    fail('--template must be one of: notes, todo, chat')
  }
  const target = join(args.dir, args.name)
  if (existsSync(target) && !args.force) {
    fail(`${target} already exists; refusing to overwrite (pass --force)`)
  }

  const plan = []

  // The degradation layer: copied verbatim from the framework (they are the
  // canonical, tested implementations — the starter must never fork them).
  for (const module of LIB_MODULES) {
    plan.push({ from: join(frameworkPwa, module), to: join(target, module), kind: 'copy' })
  }
  for (const file of ['src/app.css', 'src/globals.d.ts', 'src/wa-sqlite.d.ts', 'index.html', 'vite.config.ts', 'tsconfig.json', 'tsconfig.app.json', 'tsconfig.node.json', 'scripts/generate-sw.mjs', 'scripts/generate-icons.mjs', 'public/companion.html']) {
    const from = join(frameworkPwa, file)
    if (existsSync(from)) plan.push({ from, to: join(target, file), kind: 'copy' })
  }

  // Generated app-specific files.
  plan.push({ kind: 'generate', to: join(target, 'package.json'), content: packageJson(args.name, args.description) })
  plan.push({ kind: 'generate', to: join(target, 'src', 'main.ts'), content: readFileSync(join(frameworkPwa, 'src', 'main.ts'), 'utf8') })
  plan.push({ kind: 'generate', to: join(target, 'src', 'App.svelte'), content: appSvelte(args.template) })
  plan.push({ kind: 'generate', to: join(target, '.github', 'workflows', 'ci.yml'), content: ciWorkflow(args.name) })
  plan.push({ kind: 'generate', to: join(target, 'README.md'), content: readme(args.name, args.description, args.template) })
  plan.push({
    kind: 'generate',
    to: join(target, '.gitignore'),
    content: 'node_modules/\ndist/\n*.tsbuildinfo\n.vite/\ncoverage/\n.DS_Store\n',
  })

  if (args.dryRun) {
    console.log(`create-tpt-pwa (dry run): would scaffold ${args.template} starter at ${target}`)
    for (const item of plan) console.log(`  ${item.kind === 'copy' ? 'copy ' : 'gen  '}${item.to}`)
    return
  }

  for (const item of plan) {
    mkdirSync(dirname(item.to), { recursive: true })
    if (item.kind === 'copy') {
      cpSync(item.from, item.to)
    } else {
      writeFileSync(item.to, item.content)
    }
  }

  console.log(`create-tpt-pwa: scaffolded ${args.template} starter at ${target} (${plan.length} files)`)
  console.log('next steps:')
  console.log(`  cd ${args.name}`)
  console.log('  pnpm install && pnpm dev')
  console.log('  # optional native superpowers: run the cortex-daemon from the framework repo')
}

main()
