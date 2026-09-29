#!/usr/bin/env node
// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
// Version-sync check: every user-facing manifest in the monorepo must carry
// the same version. Run by CI (security.yml) and `pnpm run versions:check`.
import { readFileSync } from 'node:fs'

const manifests = [
  { path: 'package.json', read: () => JSON.parse(readFileSync('package.json', 'utf8')).version },
  { path: 'pwa/package.json', read: () => JSON.parse(readFileSync('pwa/package.json', 'utf8')).version },
  { path: 'cortex-shell/package.json', read: () => JSON.parse(readFileSync('cortex-shell/package.json', 'utf8')).version },
  { path: 'cortex-shell/src-tauri/tauri.conf.json', read: () => JSON.parse(readFileSync('cortex-shell/src-tauri/tauri.conf.json', 'utf8')).version },
  { path: 'cortex-shell/src-tauri/Cargo.toml', read: () => readFileSync('cortex-shell/src-tauri/Cargo.toml', 'utf8').match(/^version\s*=\s*"([^"]+)"/m)?.[1] },
  { path: 'cortex-android/app/build.gradle.kts', read: () => readFileSync('cortex-android/app/build.gradle.kts', 'utf8').match(/versionName\s*=\s*"([^"]+)"/)?.[1] },
  { path: 'cortex-engine/Cargo.toml', read: () => readFileSync('cortex-engine/Cargo.toml', 'utf8').match(/^version\s*=\s*"([^"]+)"/m)?.[1] },
]

let failed = false
const versions = new Map()
for (const manifest of manifests) {
  const version = manifest.read()
  if (!version) {
    console.error(`version-sync: ${manifest.path} has no parseable version`)
    failed = true
    continue
  }
  versions.set(version, (versions.get(version) ?? new Set()).add(manifest.path))
}

if (versions.size > 1) {
  failed = true
  console.error('version-sync: manifests disagree:')
  for (const [version, paths] of versions) {
    console.error(`  ${version}:`)
    for (const path of paths) console.error(`    ${path}`)
  }
  process.exit(1)
}
console.log(`version-sync: all manifests agree on ${[...versions.keys()][0]}`)
