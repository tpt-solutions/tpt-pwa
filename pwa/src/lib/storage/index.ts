// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { IndexedDBStorage } from './idb'
import { MemoryStorage } from './memory'
import { SQLiteStorage } from './sqlite'
import type { NoteStorage } from './types'

export { IndexedDBStorage } from './idb'
export { MemoryStorage } from './memory'
export { SQLiteStorage } from './sqlite'
export * from './types'

/**
 * Capability negotiation for storage (spec §4): pick the richest backend the
 * environment actually supports, exactly once, and hand the caller a single
 * `NoteStorage`. Application code never branches on which engine won -- the
 * entire storage concern collapses to one interface.
 *
 * Preference order:
 *   1. SQLite in Wasm on OPFS  -- real SQL, transactional, durable
 *   2. IndexedDB               -- universal fallback (spec §4 Path B)
 *   3. In-memory               -- keeps the app alive when nothing persists
 */
export async function createStorage(): Promise<NoteStorage> {
  const candidates: NoteStorage[] = [new SQLiteStorage(), new IndexedDBStorage()]
  for (const candidate of candidates) {
    if (!candidate.available) continue
    try {
      await candidate.init()
      return candidate
    } catch {
      // The candidate advertised itself as available but failed to
      // initialise (quota, private mode, broken OPFS): degrade quietly.
    }
  }
  const lastResort = new MemoryStorage()
  await lastResort.init()
  return lastResort
}
