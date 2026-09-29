// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { warnDev } from './devlog'

/**
 * Durable home for the CRDT mirror's binary snapshots, kept in a dedicated
 * IndexedDB (NOT the negotiated note storage -- the mirror must survive
 * storage-backend switches and be independent of which engine won).
 * Absent IndexedDB (tests, exotic browsers) disables persistence only; the
 * app runs the mirror in memory.
 */
const DB_NAME = 'tpt-pwa-crdt'
const STORE = 'snapshots'
const KEY = 'notes'

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open(DB_NAME, 1)
    open.onupgradeneeded = () => {
      if (!open.result.objectStoreNames.contains(STORE)) open.result.createObjectStore(STORE)
    }
    open.onsuccess = () => resolve(open.result)
    open.onerror = () => reject(open.error ?? new Error('IndexedDB open failed'))
  })
}

export async function loadCrdtSnapshot(): Promise<Uint8Array | null> {
  if (typeof indexedDB !== 'object') return null
  try {
    const db = await openDb()
    return await new Promise<Uint8Array | null>((resolve, reject) => {
      const tx = db.transaction(STORE, 'readonly')
      const request = tx.objectStore(STORE).get(KEY) as IDBRequest<Uint8Array | undefined>
      tx.oncomplete = () => resolve(request.result ?? null)
      tx.onabort = tx.onerror = () => reject(tx.error ?? new Error('crdt snapshot read failed'))
    }).finally(() => db.close())
  } catch (error) {
    warnDev('crdt', error)
    return null
  }
}

export async function saveCrdtSnapshot(binary: Uint8Array): Promise<void> {
  if (typeof indexedDB !== 'object') return
  const db = await openDb()
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(STORE, 'readwrite')
      tx.objectStore(STORE).put(binary, KEY)
      tx.oncomplete = () => resolve()
      tx.onabort = tx.onerror = () => reject(tx.error ?? new Error('crdt snapshot write failed'))
    })
  } finally {
    db.close()
  }
}

/** Best-effort persist that never throws; a failed save only logs in dev. */
export async function persistCrdtSnapshot(binary: Uint8Array): Promise<void> {
  try {
    await saveCrdtSnapshot(binary)
  } catch (error) {
    warnDev('crdt', error)
  }
}
