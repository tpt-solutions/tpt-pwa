// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { DeadLetter, Note, NoteId, NoteStorage, SyncOutboxEntry } from './types'
import { normalizeNote } from './types'

const DB_NAME = 'tpt-pwa'
const DB_VERSION = 2
const NOTES = 'notes'
const OUTBOX = 'sync_outbox'
const DEAD = 'dead_letters'

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
        if (!db.objectStoreNames.contains(DEAD)) {
          const store = db.createObjectStore(DEAD, { keyPath: 'entry.id' })
          store.createIndex('failedAt', 'failedAt')
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
    return notes
      .map(normalizeNote)
      .filter((note) => note.deletedAt === null)
      .sort((a, b) => b.updatedAt - a.updatedAt)
  }

  async getNote(id: NoteId): Promise<Note | undefined> {
    const note = await this.#request<Note | undefined>(NOTES, 'readonly', (os) => os.get(id) as IDBRequest<Note | undefined>)
    if (!note || note.deletedAt !== null) return undefined
    return normalizeNote(note)
  }

  async upsertNote(note: Note): Promise<void> {
    await this.#request(NOTES, 'readwrite', (os) => os.put(normalizeNote(note)))
  }

  async deleteNote(id: NoteId): Promise<void> {
    await this.#request(NOTES, 'readwrite', (os) => os.delete(id))
  }

  async markSyncedNote(id: NoteId, syncedAt: number, maxUpdatedAt: number): Promise<void> {
    const db = this.#mustInit()
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(NOTES, 'readwrite')
      const os = tx.objectStore(NOTES)
      const get = os.get(id)
      get.onsuccess = () => {
        const note = get.result as Note | undefined
        // Conditional inside the transaction: the stamp only lands when the
        // stored revision is the one that was pushed and not yet stamped.
        if (note && note.updatedAt <= maxUpdatedAt && note.syncedAt === null) {
          os.put({ ...note, syncedAt })
        }
      }
      tx.oncomplete = () => resolve()
      tx.onabort = tx.onerror = () => reject(tx.error ?? new Error('markSynced transaction failed'))
    })
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

  async deadLetter(entry: SyncOutboxEntry, reason: string): Promise<void> {
    const record: DeadLetter = { entry, reason, failedAt: Date.now() }
    await this.#request(DEAD, 'readwrite', (os) => os.put(record))
  }

  async deadLetters(): Promise<DeadLetter[]> {
    const letters = await this.#request<DeadLetter[]>(DEAD, 'readonly', (os) => os.getAll() as IDBRequest<DeadLetter[]>)
    return letters.sort((a, b) => b.failedAt - a.failedAt)
  }
}
