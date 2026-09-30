// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { get } from 'svelte/store'
import { checkCortexConnection, cortexRPC, CortexSyncTransport, cortexWSURL, setCortexAuthToken } from './cortex-client'
import { requestBackgroundSync } from './background-sync'
import { NoteDoc } from './crdt'
import { loadCrdtSnapshot, persistCrdtSnapshot } from './crdt-store'
import { warnDev } from './devlog'
import { createStorageWithLeaderElection } from './storage'
import type { Note, NoteId, NoteStorage } from './storage'
import { createEncryptedStorage } from './storage/encrypted'
import { atRestSlot, cryptoEnabled, cryptoUnlocked, disableCrypto, enableCrypto, getSyncKey, unlockCrypto } from './cryptostate'
import { SyncManager } from './sync'
import { summarizeTasks } from './telemetry'
import { appStatus, capabilities, cryptoStatus, daemonTasks, daemonVersion, lastFlush, notes, online, pendingSync } from './stores'

let rawStorage: NoteStorage | null = null
let storage: NoteStorage | null = null
let sync: SyncManager | null = null
let crdtDoc: NoteDoc | null = null
const saveTimers = new Map<NoteId, ReturnType<typeof setTimeout>>()
const SAVE_DEBOUNCE_MS = 400

let initPromise: Promise<void> | null = null
let finishDone = false
let listenersInstalled = false

/** App bootstrap: negotiate storage, load notes, feature-detect the daemon, arm sync triggers. Idempotent. */
export function initApp(): Promise<void> {
  initPromise ??= doInit()
  return initPromise
}

async function doInit(): Promise<void> {
  try {
    // Web Locks leader election (where available): one tab owns storage
    // decisions (backend choice, migration); others open the same database
    // in follower mode instead of silently picking a different one.
    rawStorage = await createStorageWithLeaderElection()
    storage = cryptoEnabled() ? createEncryptedStorage(rawStorage, atRestSlot) : rawStorage
    cryptoStatus.set({ enabled: cryptoEnabled(), unlocked: cryptoUnlocked() })
    capabilities.update((c) => ({ ...c, storageBackend: storage!.backend }))
    // Ask for durable storage while we're at it: without persistence the
    // browser may evict OPFS/IndexedDB under pressure, defeating the whole
    // offline-first point. Best-effort -- denial only degrades durability.
    try {
      const persisted = await navigator.storage?.persist?.()
      if (persisted === false) warnDev('app', 'storage persistence denied: offline data may be evicted under pressure')
    } catch (error) {
      warnDev('app', error)
    }

    // Encrypted but not unlocked this session: stop here. The lock screen
    // takes the passphrase, and unlockWith resumes the rest of the boot.
    if (cryptoEnabled() && !cryptoUnlocked()) {
      appStatus.set('locked')
      return
    }

    await finishInit()
  } catch (error) {
    initPromise = null // allow a retry after a failed bootstrap
    warnDev('app', error)
    appStatus.set('error')
  }
}

/**
 * The post-unlock half of the boot: load notes, arm the CRDT mirror and the
 * sync triggers, negotiate the daemon. Runs exactly once per session.
 */
async function finishInit(): Promise<void> {
  if (finishDone || !storage) return
  try {
    await refreshNotes()

    // CRDT mirror is best-effort: absence only disables multi-device merge,
    // never the app. While encryption is on the mirror stays OFF -- it
    // persists note content in its own store and is not encrypted yet, so
    // keeping it live would leak plaintext beside the encrypted rows.
    if (cryptoEnabled()) {
      capabilities.update((c) => ({ ...c, crdt: false }))
    } else {
      const snapshot = await loadCrdtSnapshot()
      crdtDoc = await NoteDoc.open(snapshot ?? undefined)
      if (crdtDoc && !snapshot) {
        // First run: seed the mirror from durable storage so a later merge
        // starts from what the user already has.
        for (const note of await storage.listNotes()) crdtDoc.upsertNote(note)
        await persistCrdt()
      }
      capabilities.update((c) => ({ ...c, crdt: crdtDoc !== null }))
    }

    installStorage(storage)
    if (typeof window !== 'undefined' && !listenersInstalled) {
      listenersInstalled = true
      window.addEventListener('online', () => {
        online.set(true)
        void flushSync()
      })
      window.addEventListener('offline', () => online.set(false))
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') void flushSync()
      })
      // The tab is going away (reload, close, background eviction): debounced
      // edits must land in durable storage NOW or they die with the page.
      window.addEventListener('pagehide', () => flushPendingSaves())
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'hidden') flushPendingSaves()
      })
      // Companion-host relay (cortex-android): the Android service forwards
      // daemon notifications and link state into this WebView as DOM events,
      // covering windows where the PWA's own WebSocket is between reconnects.
      window.addEventListener('cortex:notification', (event) => {
        const method = (event as CustomEvent<{ method?: string }>).detail?.method
        if (method === 'cortex.event.taskCompleted') void flushSync()
      })
      // The companion's embedded daemon requires a shared /rpc token; it is
      // delivered here and appended to every connect attempt from now on.
      window.addEventListener('cortex:auth', (event) => {
        const token = (event as CustomEvent<{ token?: string }>).detail?.token
        if (typeof token === 'string' && token !== '') {
          setCortexAuthToken(token)
          if (!cortexRPC.connected) void checkCortexConnection().then((connected) => {
            capabilities.update((c) => ({ ...c, cortex: connected }))
            if (connected) {
              cortexRPC.enableAutoReconnect()
              void refreshTelemetry()
              sync?.setTransport(new CortexSyncTransport())
              void flushSync()
            }
          })
        }
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
      // The daemon was really there once: keep the link alive across its
      // restarts instead of degrading until the next full app boot.
      cortexRPC.enableAutoReconnect()
      void refreshTelemetry()
      if (sync) sync.setTransport(new CortexSyncTransport())
    }

    finishDone = true
    appStatus.set('ready')
    void flushSync()
  } catch (error) {
    warnDev('app', error)
    appStatus.set('error')
  }
}

/**
 * Unlock an encrypted session: verify the passphrase, fill the key slots,
 * and run the deferred half of the boot. Resolves false on a wrong
 * passphrase; throws when encryption is not set up.
 */
export async function unlockWith(passphrase: string): Promise<boolean> {
  if (!cryptoEnabled() || finishDone) return false
  const ok = await unlockCrypto(passphrase)
  if (!ok) return false
  cryptoStatus.set({ enabled: true, unlocked: true })
  await finishInit()
  return true
}

/**
 * First-time setup (or re-enable after disabling): persist the passphrase
 * metadata and swap the encrypted storage in. Existing plaintext rows
 * re-encrypt on their next write; legacy rows keep reading transparently.
 */
export async function enableEncryption(passphrase: string): Promise<void> {
  await enableCrypto(passphrase)
  storage = createEncryptedStorage(rawStorage ?? storage!, atRestSlot)
  installStorage(storage)
  cryptoStatus.set({ enabled: true, unlocked: true })
  await refreshNotes()
}

/** Turn encryption off: verify, rewrite everything as plaintext, unseal the app. */
export async function disableEncryption(passphrase: string): Promise<void> {
  const raw = rawStorage ?? storage
  const sealed = storage
  if (!raw || !sealed) throw new Error('app not initialised')
  await disableCrypto(passphrase, raw, sealed)
  storage = raw
  installStorage(raw)
  cryptoStatus.set({ enabled: false, unlocked: false })
  await refreshNotes()
}

/** (Re)point the sync manager at the active storage. */
function installStorage(active: NoteStorage): void {
  storage = active
  sync?.stop()
  sync = new SyncManager({ storage: active, onlineTarget: typeof window !== 'undefined' ? window : null, getSyncKey })
  sync.start()
}

export async function refreshNotes(): Promise<void> {
  if (!storage) return
  notes.set(await storage.listNotes())
  pendingSync.set((await storage.pendingQueue()).length)
}

/**
 * Optimistic note creation (spec §4 scenario): the note appears instantly,
 * then persistence + outbox enqueue happen in the background. On failure the
 * optimistic insert is rolled back AND the partial durable write is undone,
 * leaving no half-created note behind when the user retries.
 */
export async function createNote(title = '', body = ''): Promise<Note> {
  if (!storage) throw new Error('app not initialised')
  const now = Date.now()
  const note: Note = { id: crypto.randomUUID(), title, body, createdAt: now, updatedAt: now, syncedAt: null, deletedAt: null }
  notes.update((list) => [note, ...list])
  let stored = false
  try {
    await storage.upsertNote(note)
    stored = true
    await sync?.queueNote(note, 'create')
    crdtDoc?.upsertNote(note)
    await persistCrdt()
    await bumpPending()
  } catch (error) {
    notes.update((list) => list.filter((n) => n.id !== note.id))
    if (stored) {
      try {
        await storage.deleteNote(note.id)
      } catch (undoError) {
        warnDev('app', undoError)
      }
    }
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
  // Monotonic local revision: a device clock that jumps backwards (NTP,
  // travel) must not make an edit look older than the note it edits.
  const updatedAt = Math.max(Date.now(), snapshot.updatedAt + 1)
  notes.update((list) => list.map((n) => (n.id === id ? { ...n, ...patch, updatedAt } : n)))

  clearTimeout(saveTimers.get(id))
  saveTimers.set(
    id,
    setTimeout(() => {
      saveTimers.delete(id)
      void persistNote(id).catch(() => {
        // Durable write failed: restore the pre-edit revision the storage
        // layer actually still holds, so UI and disk agree.
        notes.update((list) => list.map((n) => (n.id === id ? snapshot : n)))
      })
    }, SAVE_DEBOUNCE_MS),
  )
}

/** The durable write behind `updateNote`: storage + outbox + CRDT mirror. */
async function persistNote(id: NoteId): Promise<void> {
  const store = storage
  if (!store) return
  const note = get(notes).find((n) => n.id === id)
  if (!note || note.deletedAt !== null) return
  await store.upsertNote(note)
  await sync?.queueNote(note, 'update')
  crdtDoc?.upsertNote(note)
  await persistCrdt()
  await bumpPending()
}

/**
 * Persist every pending debounced edit immediately (pagehide / tab hidden).
 * Resolves when all pending writes settle; event listeners fire-and-forget.
 */
export function flushPendingSaves(): Promise<unknown> {
  const writes: Promise<unknown>[] = []
  for (const id of [...saveTimers.keys()]) {
    clearTimeout(saveTimers.get(id))
    saveTimers.delete(id)
    writes.push(persistNote(id).catch((error) => warnDev('app', error)))
  }
  return Promise.all(writes)
}

/**
 * Optimistic delete. The note is tombstoned (not hard-removed) so the delete
 * survives as data: it syncs to other devices, wins LWW against concurrent
 * edits, and the CRDT mirror converges on the deletion. Rolled back (and the
 * tombstone undone) if the durable write fails.
 */
export async function deleteNote(id: NoteId): Promise<void> {
  if (!storage) return
  const snapshot = get(notes)
  const removed = snapshot.find((n) => n.id === id)
  if (!removed) return
  // A debounced edit for this note must not fire after the delete.
  clearTimeout(saveTimers.get(id))
  saveTimers.delete(id)

  const deletedAt = Math.max(Date.now(), removed.updatedAt + 1)
  const tombstone: Note = { ...removed, deletedAt, updatedAt: deletedAt }
  notes.update((list) => list.filter((n) => n.id !== id))
  try {
    await storage.upsertNote(tombstone)
    await sync?.queueNote(removed, 'delete', deletedAt)
    crdtDoc?.removeNote(id, deletedAt)
    await persistCrdt()
    await bumpPending()
  } catch (error) {
    warnDev('app', error)
    notes.set([...snapshot.filter((n) => n.id !== id), removed].sort((a, b) => b.updatedAt - a.updatedAt))
    throw new Error('failed to delete note')
  }
}

/**
 * Merge imported notes (from a JSON/Markdown file, see `transfer.ts`) into
 * local storage. Last-writer-wins per note against what's already here:
 * newer imports overwrite, older ones are ignored, imported tombstones
 * delete. Every applied note enters the outbox like a local edit, so imports
 * propagate to other devices through the normal sync paths.
 */
export async function importNotes(incoming: Note[]): Promise<{ added: number; updated: number; deleted: number }> {
  if (!storage) throw new Error('app not initialised')
  let added = 0
  let updated = 0
  let deleted = 0

  for (const raw of incoming) {
    // Imported data is a local edit from elsewhere: never pre-stamped as synced.
    const note: Note = { ...raw, syncedAt: null }
    const local = await storage.getNote(note.id)

    if (local === undefined) {
      // A tombstone for a note we don't have deletes nothing; skip it.
      if (note.deletedAt !== null) continue
      if (!Number.isFinite(note.createdAt) || note.createdAt <= 0) note.createdAt = note.updatedAt
      if (!Number.isFinite(note.updatedAt) || note.updatedAt <= 0) note.updatedAt = Date.now()
      await storage.upsertNote(note)
      await sync?.queueNote(note, 'create')
      crdtDoc?.upsertNote(note)
      added++
    } else if (note.updatedAt > local.updatedAt) {
      if (note.deletedAt !== null) {
        // Newer tombstone: the delete wins locally and must propagate.
        const tombstone: Note = { ...local, deletedAt: note.deletedAt, updatedAt: note.deletedAt }
        await storage.upsertNote(tombstone)
        await sync?.queueNote(local, 'delete', note.deletedAt)
        crdtDoc?.removeNote(note.id, note.deletedAt)
        deleted++
      } else {
        await storage.upsertNote(note)
        await sync?.queueNote(note, 'update')
        crdtDoc?.upsertNote(note)
        updated++
      }
    }
    // else: the local copy is at least as new -- nothing to do.
  }

  await persistCrdt()
  await refreshNotes()
  await bumpPending()
  return { added, updated, deleted }
}

/** Drain the outbox now; updates the pending count and telemetry with the authoritative result. */
export async function flushSync(): Promise<void> {
  if (!sync) return
  try {
    const outcome = await sync.flush()
    pendingSync.set(outcome.pending)
    lastFlush.set(outcome)
    if (outcome.synced > 0) await refreshNotes()
    // Entries left queued (offline flush, no daemon yet): re-arm Background
    // Sync so the OS wakes the worker when connectivity returns, even if
    // this tab is gone by then.
    if (outcome.pending > 0) void requestBackgroundSync()
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

/** Save the CRDT mirror's binary; best-effort, never blocks a mutation. */
async function persistCrdt(): Promise<void> {
  if (!crdtDoc) return
  await persistCrdtSnapshot(crdtDoc.save())
}

/** Test seam: rebind the module's negotiated storage (app.test.ts drives createNote/updateNote through it). */
export function __setAppStorageForTests(replacement: NoteStorage): void {
  storage = replacement
  sync = new SyncManager({ storage: replacement })
}

/** Test seam: rebind the CRDT mirror. */
export function __setCrdtDocForTests(doc: NoteDoc | null): void {
  crdtDoc = doc
}

/** Test seam: run the durable write behind a debounced edit now. */
export const __persistNoteForTests = persistNote
