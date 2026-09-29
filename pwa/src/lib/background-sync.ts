// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { warnDev } from './devlog'

export const BACKGROUND_SYNC_TAG = 'tpt-sync'

/**
 * Register the Service Worker Background Sync tag (spec §4 Path B). When the
 * tab dies before connectivity returns, the browser wakes the worker on the
 * `sync` event and it pings any open client — without this registration the
 * worker's `sync` handler is dead code. One-shot sync retries a few times on
 * failure; re-registering after every flush that leaves entries queued keeps
 * the retry chain alive.
 */
export async function requestBackgroundSync(tag: string = BACKGROUND_SYNC_TAG): Promise<void> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  try {
    const registration = await navigator.serviceWorker.ready
    const sync = (registration as ServiceWorkerRegistration & { sync?: { register(tag: string): Promise<void> } }).sync
    if (!sync) return // Background Sync unsupported (e.g. Safari): flush triggers already cover it
    await sync.register(tag)
  } catch (error) {
    // Registration failure is never fatal: the `online` event and app-focus
    // triggers remain the fallback scheduling paths.
    warnDev('sync', error)
  }
}
