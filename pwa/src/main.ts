import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { flushSync, initApp } from './lib/app'
import { warnDev } from './lib/devlog'

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
  // Background Sync relay (spec §4 Path B): the SW wakes on `sync` and pings us.
  navigator.serviceWorker.addEventListener('message', (event) => {
    if ((event.data as { type?: string } | null)?.type === 'tpt-sync') void flushSync()
  })
}

export default app
