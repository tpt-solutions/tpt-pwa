// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { get } from 'svelte/store'
import { checkCortexConnection, cortexRPC, CortexSyncTransport } from './cortex-client'
import { NoteDoc } from './crdt'
import { warnDev } from './devlog'
import { createStorage } from './storage'
import type { Note, NoteId, NoteStorage } from './storage'
import { SyncManager } from './sync'
import { summarizeTasks } from './telemetry'
import { appStatus, capabilities, daemonTasks, daemonVersion, lastFlush, notes, online, pendingSync } from './stores'

let storage: NoteStorage | null = null
let sync: SyncManager | null = null
let crdtDoc: NoteDoc | null = null
const saveTimers = new Map<NoteId, ReturnType<typeof setTimeout>>()
const SAVE_DEBOUNCE_MS = 400

let initPromise: Promise<void> | null = null

/** App bootstrap: negotiate storage, load notes, feature-detect the daemon, arm sync triggers. Idempotent. */
export function initApp(): Promise<void> {
  initPromise ??= doInit()
  return initPromise
}

async function doInit(): Promise<void> {
  try {
    storage = await createStorage()
    capabilities.update((c) => ({ ...c, storageBackend: storage!.backend }))

    await refreshNotes()

    // CRDT mirror is best-effort: absence only disables multi-device merge, never the app.
    crdtDoc = await NoteDoc.open()
    capabilities.update((c) => ({ ...c, crdt: crdtDoc !== null }))

    sync = new SyncManager({ storage, onlineTarget: typeof window !== 'undefined' ? window : null })
    sync.start()
    if (typeof window !== 'undefined') {
      window.addEventListener('online', () => {
        online.set(true)
        void flushSync()
      })
      window.addEventListener('offline', () => online.set(false))
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') void flushSync()
      })
      online.set(navigator.onLine)
      // Companion-host relay (cortex-android): the Android service forwards
      // daemon notifications and link state into this WebView as DOM events,
      // covering windows where the PWA's own WebSocket is between reconnects.
      window.addEventListener('cortex:notification', (event) => {
        const method = (event as CustomEvent<{ method?: string }>).detail?.method
        if (method === 'cortex.event.taskCompleted') void flushSync()
      })
      window.addEventListener('cortex:state', (event) => {
        const state = (event as CustomEvent<{ state?: string }>).detail?.state
        if (state === 'connected' || state === 'disconnected') {
          capabilities.update((c) => ({ ...c, cortex: state === 'connected' }))
          if (state === 'connected') {
            void refreshTelemetry()
            sync?.setTransport(new CortexSyncTransport())
            void flushSync()
          } else {
            daemonVersion.set(null)
            daemonTasks.set(null)
            sync?.setTransport(null)
          }
        }
      })
    }

    // Capability check (spec §4): one branch point, no per-OS code anywhere.
    const cortexConnected = await checkCortexConnection()
    capabilities.update((c) => ({ ...c, cortex: cortexConnected }))
    if (cortexConnected) {
      void refreshTelemetry()
      if (sync) sync.setTransport(new CortexSyncTransport())
    }

    appStatus.set('ready')
    void flushSync()
  } catch (error) {
    initPromise = null // allow a retry after a failed bootstrap
    warnDev('app', error)
    appStatus.set('error')
  }
}

export async function refreshNotes(): Promise<void> {
  if (!storage) return
  notes.set(await storage.listNotes())
  pendingSync.set((await storage.pendingQueue()).length)
}

/**
 * Optimistic note creation (spec §4 scenario): the note appears instantly,
 * then persistence + outbox enqueue happen in the background. If the durable
 * write fails, the optimistic insert is rolled back and the error rethrown.
 */
export async function createNote(title = '', body = ''): Promise<Note> {
  if (!storage) throw new Error('app not initialised')
  const now = Date.now()
  const note: Note = { id: crypto.randomUUID(), title, body, createdAt: now, updatedAt: now, syncedAt: null }
  notes.update((list) => [note, ...list])
  try {
    await storage.upsertNote(note)
    await sync?.queueNote(note, 'create')
    crdtDoc?.upsertNote(note)
    await bumpPending()
  } catch (error) {
    notes.update((list) => list.filter((n) => n.id !== note.id))
    throw error
  }
  return note
}

/** Optimistic in-place edit; durable write (and outbox enqueue) is debounced per note. */
export function updateNote(id: NoteId, patch: Partial<Pick<Note, 'title' | 'body'>>): void {
  const store = storage
  if (!store) return
  const snapshot = get(notes).find((n) => n.id === id)
  if (!snapshot) return
  notes.update((list) => list.map((n) => (n.id === id ? { ...n, ...patch, updatedAt: Date.now() } : n)))

  clearTimeout(saveTimers.get(id))
  saveTimers.set(
    id,
    setTimeout(async () => {
      saveTimers.delete(id)
      const note = get(notes).find((n) => n.id === id)
      if (!note) return
      try {
        await store.upsertNote(note)
        await sync?.queueNote(note, 'update')
        crdtDoc?.upsertNote(note)
        await bumpPending()
      } catch (error) {
        warnDev('app', error)
        notes.update((list) => list.map((n) => (n.id === id ? snapshot : n)))
      }
    }, SAVE_DEBOUNCE_MS),
  )
}

/** Optimistic delete; rolled back (re-inserted, re-sorted) if the durable delete fails. */
export async function deleteNote(id: NoteId): Promise<void> {
  if (!storage) return
  const snapshot = get(notes)
  const removed = snapshot.find((n) => n.id === id)
  if (!removed) return
  notes.update((list) => list.filter((n) => n.id !== id))
  try {
    await storage.deleteNote(id)
    await sync?.queueNote(removed, 'delete')
    crdtDoc?.removeNote(id)
    await bumpPending()
  } catch (error) {
    warnDev('app', error)
    notes.set([...snapshot.filter((n) => n.id !== id), removed].sort((a, b) => b.updatedAt - a.updatedAt))
    throw new Error('failed to delete note')
  }
}

/** Drain the outbox now; updates the pending count and telemetry with the authoritative result. */
export async function flushSync(): Promise<void> {
  if (!sync) return
  try {
    const outcome = await sync.flush()
    pendingSync.set(outcome.pending)
    lastFlush.set(outcome)
    if (outcome.synced > 0) await refreshNotes()
  } catch (error) {
    // Flush is best-effort; entries stay queued for the next trigger.
    warnDev('sync', error)
  }
}

/**
 * Refresh the status panel's daemon telemetry: identity (via the contract's
 * ping) and the queue's shape (task.list). Best-effort -- the panel shows
 * stale data or blanks when the daemon is gone, never an error UI.
 */
export async function refreshTelemetry(): Promise<void> {
  if (!cortexRPC.connected) return
  try {
    const pong = (await cortexRPC.call<{ pong?: boolean; version?: string }>('cortex.ping')) ?? {}
    daemonVersion.set(pong.version ?? 'unknown')
    const listing = (await cortexRPC.call<{ tasks?: Array<{ state?: string }> }>('cortex.task.list')) ?? {}
    daemonTasks.set(summarizeTasks((listing.tasks ?? []).map((t) => ({ state: String(t.state ?? 'queued') }))))
  } catch (error) {
    warnDev('telemetry', error)
  }
}

async function bumpPending(): Promise<void> {
  if (!storage) return
  pendingSync.set((await storage.pendingQueue()).length)
}
