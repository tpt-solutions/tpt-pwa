// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note, NoteId, NoteStorage, SyncOutboxEntry } from './types'

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

  async init(): Promise<void> {}

  async listNotes(): Promise<Note[]> {
    return [...this.#notes.values()].sort((a, b) => b.updatedAt - a.updatedAt)
  }

  async getNote(id: NoteId): Promise<Note | undefined> {
    return this.#notes.get(id)
  }

  async upsertNote(note: Note): Promise<void> {
    this.#notes.set(note.id, { ...note })
  }

  async deleteNote(id: NoteId): Promise<void> {
    this.#notes.delete(id)
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
}
