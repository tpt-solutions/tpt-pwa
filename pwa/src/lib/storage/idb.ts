// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note, NoteId, NoteStorage, SyncOutboxEntry } from './types'

const DB_NAME = 'tpt-pwa'
const DB_VERSION = 1
const NOTES = 'notes'
const OUTBOX = 'sync_outbox'

/**
 * Fallback on-device store (spec §4 Path B): plain IndexedDB, the lowest
 * common denominator available in every browser. Selected by the capability
 * negotiation layer when the Wasm SQLite/OPFS path is unusable (e.g. older
 * Safari, private modes that disable OPFS).
 */
export class IndexedDBStorage implements NoteStorage {
  readonly backend = 'indexeddb' as const
  readonly available = typeof indexedDB === 'object'

  #db: IDBDatabase | null = null
  #ready: Promise<void> | null = null

  async init(): Promise<void> {
    this.#ready ??= this.#bootstrap()
    await this.#ready
  }

  async #bootstrap(): Promise<void> {
    this.#db = await new Promise<IDBDatabase>((resolve, reject) => {
      const open = indexedDB.open(DB_NAME, DB_VERSION)
      open.onupgradeneeded = () => {
        const db = open.result
        if (!db.objectStoreNames.contains(NOTES)) {
          const store = db.createObjectStore(NOTES, { keyPath: 'id' })
          store.createIndex('updatedAt', 'updatedAt')
        }
        if (!db.objectStoreNames.contains(OUTBOX)) {
          const store = db.createObjectStore(OUTBOX, { keyPath: 'id' })
          store.createIndex('queuedAt', 'queuedAt')
        }
      }
      open.onsuccess = () => resolve(open.result)
      open.onerror = () => reject(open.error ?? new Error('IndexedDB open failed'))
    })
  }

  #mustInit(): IDBDatabase {
    if (!this.#db) throw new Error('IndexedDBStorage not initialised; call init() first')
    return this.#db
  }

  #request<T>(store: string, mode: IDBTransactionMode, run: (os: IDBObjectStore) => IDBRequest<T>): Promise<T> {
    const db = this.#mustInit()
    return new Promise<T>((resolve, reject) => {
      const tx = db.transaction(store, mode)
      const request = run(tx.objectStore(store))
      tx.oncomplete = () => resolve(request.result)
      tx.onabort = tx.onerror = () => reject(tx.error ?? new Error(`IndexedDB transaction on "${store}" failed`))
    })
  }

  async listNotes(): Promise<Note[]> {
    const notes = await this.#request<Note[]>(NOTES, 'readonly', (os) => os.getAll() as IDBRequest<Note[]>)
    return notes.sort((a, b) => b.updatedAt - a.updatedAt)
  }

  async getNote(id: NoteId): Promise<Note | undefined> {
    return this.#request<Note | undefined>(NOTES, 'readonly', (os) => os.get(id) as IDBRequest<Note | undefined>)
  }

  async upsertNote(note: Note): Promise<void> {
    await this.#request(NOTES, 'readwrite', (os) => os.put(note))
  }

  async deleteNote(id: NoteId): Promise<void> {
    await this.#request(NOTES, 'readwrite', (os) => os.delete(id))
  }

  async enqueue(entry: SyncOutboxEntry): Promise<void> {
    await this.#request(OUTBOX, 'readwrite', (os) => os.put(entry))
  }

  async dequeue(id: string): Promise<void> {
    await this.#request(OUTBOX, 'readwrite', (os) => os.delete(id))
  }

  async pendingQueue(): Promise<SyncOutboxEntry[]> {
    const entries = await this.#request<SyncOutboxEntry[]>(OUTBOX, 'readonly', (os) => os.getAll() as IDBRequest<SyncOutboxEntry[]>)
    return entries.sort((a, b) => a.queuedAt - b.queuedAt)
  }
}
