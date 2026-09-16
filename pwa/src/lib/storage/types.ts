// Copyright 2026 tpt-pwa contributors. Dual-licensed MIT OR Apache-2.0.
export type NoteId = string

export type Note = {
  id: NoteId
  title: string
  body: string
  createdAt: number
  updatedAt: number
  syncedAt: number | null
}

export type SyncOutboxAction = 'create' | 'update' | 'delete'

export type SyncOutboxEntry<A extends SyncOutboxAction = SyncOutboxAction> = {
  id: string
  action: A
  kind: 'note'
  payload: A extends 'delete' ? { id: NoteId } : Note
  queuedAt: number
}

/**
 * Single storage contract, implemented three ways (SQLite/wa-sqlite on OPFS,
 * IndexedDB, in-memory for tests). The capability-negotiation layer picks ONE
 * backend at startup and the application never branches on storage again --
 * dev/type-safe, and the entire "local persistence" concern collapses to a
 * single interface (spec §5 "wa-sqlite + tpt-cortex Sync").
 */
export interface NoteStorage {
  readonly backend: 'sqlite' | 'indexeddb' | 'memory'
  readonly available: boolean
  init(): Promise<void>
  listNotes(): Promise<Note[]>
  getNote(id: NoteId): Promise<Note | undefined>
  upsertNote(note: Note): Promise<void>
  deleteNote(id: NoteId): Promise<void>
  enqueue(entry: SyncOutboxEntry): Promise<void>
  dequeue(id: string): Promise<void>
  pendingQueue(): Promise<SyncOutboxEntry[]>
}

export class StorageError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'StorageError'
  }
}
