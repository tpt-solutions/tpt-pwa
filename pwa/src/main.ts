import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { flushSync, initApp } from './lib/app'
import { warnDev } from './lib/devlog'
import { swUpdateReady } from './lib/stores'

const app = mount(App, {
  target: document.getElementById('app')!,
})

void initApp()

// Offline-first app shell: the generated Service Worker (scripts/generate-sw.mjs)
// precaches the build and serves it cache-first (spec §5). Registered only in
// production builds so HMR stays untouched during development.
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch((error) => warnDev('sw', error))
  })
  // The worker never skipWaiting()s mid-session (that would swap app code
  // under a running UI); it announces a waiting update instead and the user
  // chooses the moment via the header chip.
  navigator.serviceWorker.addEventListener('message', (event) => {
    const type = (event.data as { type?: string } | null)?.type
    if (type === 'tpt-sync') void flushSync()
    if (type === 'tpt-sw-update') swUpdateReady.set(true)
  })
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    // The user accepted the update: the activated worker now controls the
    // page, so reload once onto the new asset set.
    if (sessionStorage.getItem('tpt-sw-reloading') === '1') {
      sessionStorage.removeItem('tpt-sw-reloading')
      window.location.reload()
    }
  })
}

/** Apply a waiting service-worker update (wired to the App.svelte chip). */
export function applySwUpdate(): void {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return
  sessionStorage.setItem('tpt-sw-reloading', '1')
  navigator.serviceWorker.controller?.postMessage({ type: 'tpt-sw-skip-waiting' })
}

export default app
