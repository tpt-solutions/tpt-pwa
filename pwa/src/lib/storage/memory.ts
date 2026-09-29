// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { DeadLetter, Note, NoteId, NoteStorage, SyncOutboxEntry } from './types'
import { normalizeNote } from './types'

/**
 * Volatile last-resort store. Selected only when neither SQLite/OPFS nor
 * IndexedDB can initialise, so the app still runs (session-only persistence)
 * instead of crashing; also the deterministic backend for unit tests.
 */
export class MemoryStorage implements NoteStorage {
  readonly backend = 'memory' as const
  readonly available = true

  #notes = new Map<NoteId, Note>()
  #queue = new Map<string, SyncOutboxEntry>()
  #dead = new Map<string, DeadLetter>()

  async init(): Promise<void> {}

  async listNotes(): Promise<Note[]> {
    return [...this.#notes.values()]
      .map(normalizeNote)
      .filter((note) => note.deletedAt === null)
      .sort((a, b) => b.updatedAt - a.updatedAt)
  }

  async getNote(id: NoteId): Promise<Note | undefined> {
    const note = this.#notes.get(id)
    if (!note || note.deletedAt !== null) return undefined
    return normalizeNote(note)
  }

  async upsertNote(note: Note): Promise<void> {
    this.#notes.set(note.id, normalizeNote(note))
  }

  async deleteNote(id: NoteId): Promise<void> {
    this.#notes.delete(id)
  }

  async markSyncedNote(id: NoteId, syncedAt: number, maxUpdatedAt: number): Promise<void> {
    const note = this.#notes.get(id)
    if (!note || note.updatedAt > maxUpdatedAt || note.syncedAt !== null) return
    this.#notes.set(id, { ...note, syncedAt })
  }

  async enqueue(entry: SyncOutboxEntry): Promise<void> {
    this.#queue.set(entry.id, structuredClone(entry))
  }

  async dequeue(id: string): Promise<void> {
    this.#queue.delete(id)
  }

  async pendingQueue(): Promise<SyncOutboxEntry[]> {
    return [...this.#queue.values()].sort((a, b) => a.queuedAt - b.queuedAt)
  }

  async deadLetter(entry: SyncOutboxEntry, reason: string): Promise<void> {
    this.#dead.set(entry.id, { entry: structuredClone(entry), reason, failedAt: Date.now() })
  }

  async deadLetters(): Promise<DeadLetter[]> {
    return [...this.#dead.values()].sort((a, b) => b.failedAt - a.failedAt)
  }
}
