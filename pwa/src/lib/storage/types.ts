// Copyright 2026 tpt-pwa contributors. Dual-licensed MIT OR Apache-2.0.
export type NoteId = string

export type Note = {
  id: NoteId
  title: string
  body: string
  createdAt: number
  updatedAt: number
  syncedAt: number | null
  /**
   * Tombstone (delete-safety): when set, the note is deleted. Deletes are
   * recorded as tombstones rather than removals so they survive as data --
   * a hard delete loses to a concurrent edit on another device (the edit's
   * newer `updatedAt` resurrects it) and can never be replayed to the sync
   * endpoint. Tombstones carry `updatedAt` at delete time, so LWW orderings
   * see the delete as the newest write.
   */
  deletedAt: number | null
}

export type SyncOutboxAction = 'create' | 'update' | 'delete'

export type SyncOutboxEntry<A extends SyncOutboxAction = SyncOutboxAction> = {
  id: string
  action: A
  kind: 'note'
  payload: A extends 'delete' ? { id: NoteId; deletedAt: number } : Note
  queuedAt: number
}

/** An outbox entry the sync endpoint permanently rejected (4xx, not 408/429). */
export type DeadLetter = {
  entry: SyncOutboxEntry
  reason: string
  failedAt: number
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
  /** Live (non-tombstoned) notes, newest first. */
  listNotes(): Promise<Note[]>
  /** Returns `undefined` for missing AND tombstoned notes. */
  getNote(id: NoteId): Promise<Note | undefined>
  upsertNote(note: Note): Promise<void>
  /** Hard removal; ordinary deletes go through `upsertNote` with a tombstone. */
  deleteNote(id: NoteId): Promise<void>
  /**
   * Stamp `syncedAt` on the note in ONE storage operation, and only when the
   * stored revision is the one (or older than the one) the outbox entry
   * actually pushed (`updatedAt <= maxUpdatedAt`) and no sync was stamped
   * yet. A queued payload may be stale behind a newer local edit; this
   * guarantees the stamp can never drag content back or falsely mark a newer
   * revision as synced.
   */
  markSyncedNote(id: NoteId, syncedAt: number, maxUpdatedAt: number): Promise<void>
  enqueue(entry: SyncOutboxEntry): Promise<void>
  dequeue(id: string): Promise<void>
  pendingQueue(): Promise<SyncOutboxEntry[]>
  /** Park an entry the endpoint permanently rejected; it never retries. */
  deadLetter(entry: SyncOutboxEntry, reason: string): Promise<void>
  deadLetters(): Promise<DeadLetter[]>
}

export class StorageError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'StorageError'
  }
}

/** Coerce a legacy/foreign record into the current `Note` shape. */
export function normalizeNote(raw: Note): Note {
  return {
    id: String(raw.id),
    title: String(raw.title ?? ''),
    body: String(raw.body ?? ''),
    createdAt: Number(raw.createdAt ?? 0),
    updatedAt: Number(raw.updatedAt ?? 0),
    syncedAt: typeof raw.syncedAt === 'number' ? raw.syncedAt : null,
    deletedAt: typeof raw.deletedAt === 'number' ? raw.deletedAt : null,
  }
}
