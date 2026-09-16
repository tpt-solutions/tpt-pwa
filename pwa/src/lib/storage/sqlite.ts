// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note, NoteId, NoteStorage, SyncOutboxEntry } from './types'

type WaSQLite = typeof import('wa-sqlite')
/** The wa-sqlite type defs expose SQLiteAPI as an ambient global interface; derive it instead of importing it. */
type SQLiteAPI = ReturnType<WaSQLite['Factory']>

/**
 * Primary on-device store: SQLite compiled to Wasm ("wa-sqlite") backed by the
 * Origin Private File System VFS. The Wasm module and the JS glue are loaded
 * on demand (dynamic import, nothing inlined into the app shell bundle), so
 * complex relational queries live in a real engine while the initial payload
 * stays small. This is the first-principles antidote to pulling in a whole JS
 * data layer (spec §5 "First-Principles Dependencies").
 */
export class SQLiteStorage implements NoteStorage {
  readonly backend = 'sqlite' as const
  readonly available =
    typeof WebAssembly === 'object' &&
    typeof navigator !== 'undefined' &&
    !!navigator.storage?.getDirectory

  #instance: { sqlite3: SQLiteAPI; db: number } | null = null
  #ready: Promise<void> | null = null

  async init(): Promise<void> {
    this.#ready ??= this.#bootstrap()
    await this.#ready
  }

  async #bootstrap(): Promise<void> {
    const [{ default: SQLiteAsyncESMFactory }, { OriginPrivateFileSystemVFS }, SQLite] = await Promise.all([
      import('wa-sqlite/dist/wa-sqlite-async.mjs'),
      import('wa-sqlite/src/examples/OriginPrivateFileSystemVFS.js'),
      import('wa-sqlite'),
    ])
    const module = await SQLiteAsyncESMFactory()
    const sqlite3 = SQLite.Factory(module)
    const vfs = new OriginPrivateFileSystemVFS()
    sqlite3.vfs_register(vfs as unknown as Parameters<typeof sqlite3.vfs_register>[0], true)
    const db = await sqlite3.open_v2('tpt-pwa.sqlite3')
    await sqlite3.exec(db, SCHEMA)
    this.#instance = { sqlite3, db }
  }

  #mustInit(): { sqlite3: SQLiteAPI; db: number } {
    if (!this.#instance) throw new Error('SQLiteStorage not initialised; call init() first')
    return this.#instance
  }

  async listNotes(): Promise<Note[]> {
    const { sqlite3, db } = this.#mustInit()
    const { rows } = await sqlite3.execWithParams(
      db,
      `SELECT id, title, body, createdAt, updatedAt, syncedAt FROM notes ORDER BY updatedAt DESC`,
    )
    return rows.map(rowToNote)
  }

  async getNote(id: NoteId): Promise<Note | undefined> {
    const { sqlite3, db } = this.#mustInit()
    const { rows } = await sqlite3.execWithParams(
      db,
      `SELECT id, title, body, createdAt, updatedAt, syncedAt FROM notes WHERE id = ?`,
      [id],
    )
    return rows.length > 0 ? rowToNote(rows[0]) : undefined
  }

  async upsertNote(note: Note): Promise<void> {
    const { sqlite3, db } = this.#mustInit()
    await sqlite3.execWithParams(
      db,
      `INSERT INTO notes (id, title, body, createdAt, updatedAt, syncedAt)
       VALUES (?, ?, ?, ?, ?, ?)
       ON CONFLICT(id) DO UPDATE SET
         title = excluded.title,
         body = excluded.body,
         updatedAt = excluded.updatedAt,
         syncedAt = excluded.syncedAt`,
      [note.id, note.title, note.body, note.createdAt, note.updatedAt, note.syncedAt ?? null],
    )
  }

  async deleteNote(id: NoteId): Promise<void> {
    const { sqlite3, db } = this.#mustInit()
    await sqlite3.execWithParams(db, `DELETE FROM notes WHERE id = ?`, [id])
  }

  async enqueue(entry: SyncOutboxEntry): Promise<void> {
    const { sqlite3, db } = this.#mustInit()
    await sqlite3.execWithParams(
      db,
      `INSERT OR REPLACE INTO sync_outbox (id, kind, action, payload, queuedAt)
       VALUES (?, ?, ?, ?, ?)`,
      [entry.id, entry.kind, entry.action, JSON.stringify(entry.payload), entry.queuedAt],
    )
  }

  async dequeue(id: string): Promise<void> {
    const { sqlite3, db } = this.#mustInit()
    await sqlite3.execWithParams(db, `DELETE FROM sync_outbox WHERE id = ?`, [id])
  }

  async pendingQueue(): Promise<SyncOutboxEntry[]> {
    const { sqlite3, db } = this.#mustInit()
    const { rows } = await sqlite3.execWithParams(
      db,
      `SELECT id, kind, action, payload, queuedAt FROM sync_outbox ORDER BY queuedAt ASC`,
    )
    return rows.map((row: unknown[]) => ({
      id: String(row[0]),
      kind: row[1] as 'note',
      action: row[2] as 'create' | 'update' | 'delete',
      payload: JSON.parse(String(row[3])),
      queuedAt: Number(row[4]),
    }))
  }
}

const SCHEMA = `
  CREATE TABLE IF NOT EXISTS notes (
    id        TEXT PRIMARY KEY,
    title     TEXT NOT NULL,
    body      TEXT NOT NULL,
    createdAt INTEGER NOT NULL,
    updatedAt INTEGER NOT NULL,
    syncedAt  INTEGER
  );
  CREATE INDEX IF NOT EXISTS idx_notes_updatedAt ON notes(updatedAt);
  CREATE TABLE IF NOT EXISTS sync_outbox (
    id       TEXT PRIMARY KEY,
    kind     TEXT NOT NULL,
    action   TEXT NOT NULL,
    payload  TEXT NOT NULL,
    queuedAt INTEGER NOT NULL
  );
  CREATE INDEX IF NOT EXISTS idx_outbox_queuedAt ON sync_outbox(queuedAt);
`

function rowToNote(row: unknown[]): Note {
  return {
    id: String(row[0]),
    title: String(row[1]),
    body: String(row[2]),
    createdAt: Number(row[3]),
    updatedAt: Number(row[4]),
    syncedAt: row[5] === null || row[5] === undefined ? null : Number(row[5]),
  }
}
