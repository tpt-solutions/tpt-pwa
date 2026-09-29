// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { buildWorker, type SwAsset } from '../../scripts/generate-sw.mjs'

const assets: SwAsset[] = [
  { path: 'assets/index-abc123.js', content: 'console.log(1)' },
  { path: 'index.html', content: '<html></html>' },
]

describe('generate-sw (custom minimal service worker)', () => {
  it('precaches the hashed build output and the app shell', () => {
    const worker = buildWorker(assets)
    expect(worker).toContain('"/assets/index-abc123.js"')
    expect(worker).toContain('"/index.html"')
    expect(worker).toContain('cache.addAll')
  })

  it('the cache revision hashes file CONTENTS, not names or sizes', () => {
    const same = buildWorker(assets).match(/tpt-pwa-([0-9a-f]+)/)![1]
    const renamed = buildWorker([assets[0], { path: 'index.html', content: '<html></html> ' }]).match(/tpt-pwa-([0-9a-f]+)/)![1]
    expect(renamed).not.toBe(same)
    // A name-only change with identical bytes must NOT bump the revision.
    const sameBytesNewName = buildWorker([{ path: 'assets/index-XYZ789.js', content: 'console.log(1)' }, assets[1]])
    expect(sameBytesNewName).toContain('assets/index-XYZ789.js')
    expect(sameBytesNewName.match(/tpt-pwa-([0-9a-f]+)/)![1]).not.toBe(same)
  })

  it('a byte-identical rebuild reuses the same cache revision', () => {
    const a = buildWorker(assets).match(/tpt-pwa-([0-9a-f]+)/)![1]
    const b = buildWorker([...assets]).match(/tpt-pwa-([0-9a-f]+)/)![1]
    expect(a).toBe(b)
  })

  it('never serves /sw.js from the fetch handler', () => {
    const worker = buildWorker(assets)
    expect(worker).toContain("url.pathname === '/sw.js'")
  })

  it('does not skipWaiting on install; the user applies the update', () => {
    const worker = buildWorker(assets)
    const install = worker.slice(worker.indexOf("addEventListener('install'"), worker.indexOf("addEventListener('activate'"))
    expect(install).not.toContain('self.skipWaiting')
    expect(install).toContain('tpt-sw-update')
    expect(worker).toContain('tpt-sw-skip-waiting')
  })
})
