// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { IndexedDBStorage } from './idb'
import { warnDev } from '../devlog'
import { MemoryStorage } from './memory'
import { SQLiteStorage } from './sqlite'
import type { DeadLetter, NoteStorage, SyncOutboxEntry } from './types'

export { IndexedDBStorage } from './idb'
export { MemoryStorage } from './memory'
export { SQLiteStorage } from './sqlite'
export * from './types'

export type PersistentBackend = 'sqlite' | 'indexeddb'

const BACKEND_KEY = 'tpt-pwa.backend'

function readSavedBackend(): PersistentBackend | null {
  try {
    const saved = typeof localStorage !== 'undefined' ? localStorage.getItem(BACKEND_KEY) : null
    return saved === 'sqlite' || saved === 'indexeddb' ? saved : null
  } catch {
    return null
  }
}

function saveBackend(backend: PersistentBackend): void {
  try {
    localStorage?.setItem(BACKEND_KEY, backend)
  } catch {
    /* storage metadata is an optimization, never a hard dependency */
  }
}

export type CreateStorageOptions = {
  /**
   * Follower mode (second tab under the Web Locks leader election): open the
   * persisted backend if possible but never switch backends, migrate, or
   * overwrite the persisted choice -- the leader tab owns those decisions, so
   * a second tab can't silently move the app onto a different database.
   */
  follower?: boolean
}

/**
 * Capability negotiation for storage (spec §4): pick the richest backend the
 * environment actually supports, exactly once, and hand the caller a single
 * `NoteStorage`. Application code never branches on which engine won -- the
 * entire storage concern collapses to one interface.
 *
 * Preference order (with the previously persisted choice pinned first so the
 * app keeps the same database across boots):
 *   1. SQLite in Wasm on OPFS  -- real SQL, transactional, durable
 *   2. IndexedDB               -- universal fallback (spec §4 Path B)
 *   3. In-memory               -- keeps the app alive when nothing persists
 *
 * When the persisted choice differs from the freshly negotiated winner (e.g.
 * IndexedDB was chosen years ago and this browser has since gained OPFS),
 * the old backend's data migrates into the new one before the switch is
 * persisted.
 */
export async function createStorage(options: CreateStorageOptions = {}): Promise<NoteStorage> {
  const saved = readSavedBackend()
  const candidates: NoteStorage[] =
    saved === 'indexeddb' ? [new IndexedDBStorage(), new SQLiteStorage()] : [new SQLiteStorage(), new IndexedDBStorage()]

  let winner: NoteStorage | null = null
  for (const candidate of candidates) {
    if (!candidate.available) continue
    try {
      await candidate.init()
      winner = candidate
      break
    } catch (error) {
      // The candidate advertised itself as available but failed to
      // initialise (quota, private mode, broken OPFS): degrade quietly.
      warnDev('storage', error)
    }
  }

  if (!winner) {
    const lastResort = new MemoryStorage()
    await lastResort.init()
    return lastResort
  }

  if (options.follower) {
    // Same database as the leader or nothing; never rewrite the choice.
    return winner
  }

  // The persisted choice lost negotiation (its engine stopped working), and
  // this tab leads storage decisions: migrate what the old backend still
  // holds into the winner before repointing the app.
  if (saved && saved !== winner.backend) {
    const previous = candidates.find((candidate) => candidate.backend === saved)
    if (previous?.available) {
      try {
        await previous.init()
        await migrateStorage(previous, winner)
      } catch (error) {
        warnDev('storage', error)
      }
    }
  }

  saveBackend(winner.backend as PersistentBackend)
  return winner
}

/**
 * Copy every live note, outbox entry, and dead letter from one backend into
 * another. Tombstone-only rows (no live revision) don't migrate across
 * engines -- their deletes are already reflected in the CRDT mirror -- but
 * live notes keep their tombstone flag.
 */
export async function migrateStorage(from: NoteStorage, to: NoteStorage): Promise<void> {
  const [notes, outbox, dead] = await Promise.all([
    from.listNotes(),
    from.pendingQueue(),
    from.deadLetters().catch(() => [] as DeadLetter[]),
  ])
  for (const note of notes) await to.upsertNote(note)
  for (const entry of outbox) await to.enqueue(entry)
  for (const letter of dead) await to.deadLetter(letter.entry, letter.reason)
}

/**
 * Elect a single storage-deciding tab via the Web Locks API when available.
 * The leader runs full negotiation (and may switch/migrate backends); any
 * second tab opens in follower mode so both tabs land on the same database
 * instead of silently diverging. Without Web Locks every tab leads (the
 * pre-existing behavior).
 */
export async function createStorageWithLeaderElection(): Promise<NoteStorage> {
  const locks = typeof navigator !== 'undefined' ? navigator.locks : undefined
  if (!locks?.request) return createStorage()
  return locks.request('tpt-pwa-storage-leader', { ifAvailable: true }, async (lock) =>
    lock ? createStorage() : createStorage({ follower: true }),
  )
}
