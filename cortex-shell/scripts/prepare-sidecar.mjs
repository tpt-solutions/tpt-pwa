// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
// Prepares src-tauri/binaries/cortex-daemon-<target-triple>(.exe) for Tauri's
// externalBin. Requires a real daemon binary: it looks in
// cortex-daemon/bin/cortex-daemon-<triple>(.exe) first (what CI builds), then
// cortex-daemon/cortex-daemon(.exe) (a local `go build`), and exits non-zero
// when neither exists. A 0-byte placeholder silently produces an installer
// that can't start the daemon, so that failure mode is opt-in:
//
//   CORTEX_SIDECAR_ALLOW_PLACEHOLDER=1 pnpm install   # or: node scripts/prepare-sidecar.mjs --placeholder
//
// To build the daemon for this machine:
//   cd cortex-daemon && go build -o bin/cortex-daemon-<triple> ./cmd/cortex-daemon
import { execSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const shellDir = fileURLToPath(new URL('..', import.meta.url))
const binariesDir = join(shellDir, 'src-tauri', 'binaries')

function targetTriple() {
  if (process.env.TAURI_ENV_TARGET_TRIPLE) return process.env.TAURI_ENV_TARGET_TRIPLE
  const info = execSync('rustc -vV', { encoding: 'utf8' })
  return info.match(/host:\s*(\S+)/)?.[1] ?? ''
}

const triple = targetTriple()
if (!triple) {
  console.error('prepare-sidecar: could not determine rust host triple')
  process.exit(1)
}

const ext = triple.includes('windows') ? '.exe' : ''
const destination = join(binariesDir, `cortex-daemon-${triple}${ext}`)
mkdirSync(binariesDir, { recursive: true })

const candidates = [
  join(shellDir, '..', 'cortex-daemon', 'bin', `cortex-daemon-${triple}${ext}`),
  join(shellDir, '..', 'cortex-daemon', 'cortex-daemon' + ext),
]
const real = candidates.find((path) => existsSync(path))
if (real) {
  copyFileSync(real, destination)
  console.log(`prepare-sidecar: copied ${real} -> ${destination}`)
} else if (process.env.CORTEX_SIDECAR_ALLOW_PLACEHOLDER === '1' || process.argv.includes('--placeholder')) {
  writeFileSync(destination, '')
  console.warn('prepare-sidecar: PLACEHOLDER (0 bytes) written to ' + destination)
  console.warn('  Bundling this shell ships without a working daemon.')
  console.warn('  Build cortex-daemon first: cd cortex-daemon && go build -o bin/cortex-daemon-' + triple + ext + ' ./cmd/cortex-daemon')
} else {
  console.error(`prepare-sidecar: no cortex-daemon binary found for ${triple}`)
  console.error(`  looked in:`)
  for (const path of candidates) console.error(`    ${path}`)
  console.error(`  build it with:`)
  console.error(`    cd cortex-daemon && go build -o bin/cortex-daemon-${triple}${ext} ./cmd/cortex-daemon`)
  console.error(`  (set CORTEX_SIDECAR_ALLOW_PLACEHOLDER=1 to write a 0-byte placeholder for cargo check only)`)
  process.exit(1)
}
