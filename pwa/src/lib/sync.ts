// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note, NoteStorage, SyncOutboxAction, SyncOutboxEntry } from './storage'

/**
 * How completed outbox entries leave the device. Two implementations exist,
 * chosen by capability negotiation, never by OS sniffing (spec §4):
 *  - CortexSyncTransport (cortex-client.ts): the daemon's persistent queue.
 *  - this module's HTTP path: direct POST while the tab is alive.
 */
export interface SyncTransport {
  readonly available: boolean
  enqueueSyncTask(entries: SyncOutboxEntry[]): Promise<void>
}

export type FlushOutcome = {
  via: 'cortex' | 'http' | 'none'
  synced: number
  failed: number
  pending: number
}

export const DEFAULT_SYNC_ENDPOINT = 'https://api.tpt/sync'

export interface SyncDeps {
  storage: NoteStorage
  /** When null/absent (or its `available` is false) the manager degrades to the HTTP fallback. */
  transport?: SyncTransport | null
  fetchFn?: typeof fetch
  /** Receives `online` events; injectable so tests can use a bare EventTarget. */
  onlineTarget?: EventTarget | null
  endpoint?: string
}

/**
 * The sync engine behind spec §4's "Background Data Sync" scenario. Notes are
 * written locally first (optimistic UI never waits on the network) and every
 * unsynced change lands in the storage layer's durable outbox. `flush()`
 * drains the outbox via the best available path and is idempotent + safe to
 * call repeatedly: entries are only removed after a confirmed hand-off, so a
 * crash or offline flush merely leaves them queued for the next attempt.
 */
export class SyncManager {
  readonly #storage: NoteStorage
  #transport: SyncTransport | null
  readonly #fetchFn: typeof fetch
  readonly #endpoint: string
  readonly #onlineTarget: EventTarget | null
  #chain: Promise<FlushOutcome> = Promise.resolve({ via: 'none', synced: 0, failed: 0, pending: 0 })
  #started = false

  constructor(deps: SyncDeps) {
    this.#storage = deps.storage
    this.#transport = deps.transport ?? null
    this.#fetchFn = deps.fetchFn ?? fetch
    this.#endpoint = deps.endpoint ?? DEFAULT_SYNC_ENDPOINT
    this.#onlineTarget = deps.onlineTarget ?? null
  }

  setTransport(transport: SyncTransport | null): void {
    this.#transport = transport
  }

  /** Attach the `online` listener (spec §4 Path B: `window.addEventListener('online', triggerSync)`). */
  start(): void {
    if (this.#started || !this.#onlineTarget) return
    this.#started = true
    this.#onlineTarget.addEventListener('online', this.#triggerFlush)
  }

  stop(): void {
    if (!this.#started) return
    this.#started = false
    this.#onlineTarget?.removeEventListener('online', this.#triggerFlush)
  }

  #triggerFlush = (): void => {
    void this.flush().catch(() => {})
  }

  /** Durably queue one note mutation. `delete` carries only the id. */
  async queueNote(note: Note, action: SyncOutboxAction): Promise<void> {
    const entry: SyncOutboxEntry =
      action === 'delete'
        ? { id: `${note.id}:delete`, kind: 'note', action: 'delete', payload: { id: note.id }, queuedAt: Date.now() }
        : { id: `${note.id}:${action}`, kind: 'note', action, payload: note, queuedAt: Date.now() }
    await this.#storage.enqueue(entry)
  }

  /**
   * Drain the outbox. Serialized internally so overlapping triggers (online
   * event, visibility change, post-enqueue) can't double-send an entry.
   */
  flush(): Promise<FlushOutcome> {
    this.#chain = this.#chain.then(
      () => this.#flushOnce(),
      () => this.#flushOnce(), // a failed attempt must not poison the next one
    )
    return this.#chain
  }

  async #flushOnce(): Promise<FlushOutcome> {
    const entries = await this.#storage.pendingQueue()
    if (entries.length === 0) return { via: 'none', synced: 0, failed: 0, pending: 0 }

    // Path A (spec §4): daemon connected -- delegate and let it own retries.
    // The PWA can be closed immediately after this resolves.
    if (this.#transport?.available) {
      try {
        await this.#transport.enqueueSyncTask(entries)
        await Promise.all(entries.map((entry) => this.#storage.dequeue(entry.id)))
        return { via: 'cortex', synced: entries.length, failed: 0, pending: 0 }
      } catch {
        // Daemon accepted the connection but dropped mid-handoff: degrade.
      }
    }

    // Path B (spec §4): vanilla browser -- push directly while online; on
    // failure entries stay queued until the next `online` event / app open.
    let synced = 0
    let failed = 0
    for (const entry of entries) {
      try {
        const response = await this.#fetchFn(this.#endpoint, {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ action: entry.action, note: entry.payload }),
        })
        if (!response.ok) throw new Error(`sync endpoint returned HTTP ${response.status}`)
        await this.#storage.dequeue(entry.id)
        if (entry.action !== 'delete' && 'title' in entry.payload) {
          await this.#storage.upsertNote({ ...entry.payload, syncedAt: Date.now() })
        }
        synced++
      } catch {
        failed++
      }
    }
    return { via: 'http', synced, failed, pending: failed }
  }
}
