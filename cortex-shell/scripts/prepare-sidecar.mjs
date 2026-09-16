// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
// Prepares src-tauri/binaries/cortex-daemon-<target-triple>(.exe) for Tauri's
// externalBin. Prefers a real daemon binary if a build is found; otherwise
// creates a placeholder so `cargo check` / `tauri dev` can run. Placeholder
// is 0 bytes -- bundling a release requires building the daemon first
// (see README).
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
} else {
  writeFileSync(destination, '')
  console.log(`prepare-sidecar: placeholder written to ${destination}`)
  console.log('  (0 bytes -- build cortex-daemon for releases; see README)')
}
