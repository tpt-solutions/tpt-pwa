// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note, NoteStorage, SyncOutboxAction, SyncOutboxEntry } from './storage'
import { warnDev } from './devlog'
import { encryptBox } from './crypto'

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

/**
 * There is no managed sync service behind this repo, so the direct-HTTP
 * fallback ships DISABLED: `null` endpoint means "hand-off to the daemon or
 * stay queued". Deployments that run a sync endpoint pass its URL via
 * `SyncDeps.endpoint` (and ideally a same-origin path, given CORS).
 */
export const DEFAULT_SYNC_ENDPOINT: string | null = null

export interface SyncDeps {
  storage: NoteStorage
  /** When null/absent (or its `available` is false) the manager degrades to the HTTP fallback. */
  transport?: SyncTransport | null
  fetchFn?: typeof fetch
  /** Receives `online` events; injectable so tests can use a bare EventTarget. */
  onlineTarget?: EventTarget | null
  /** Direct-HTTP fallback endpoint; `null` (the default) disables that path. */
  endpoint?: string | null
  /**
   * The session's sync key (end-to-end encryption). When it returns a key,
   * every entry payload's title/body are sealed with it BEFORE any hand-off
   * -- the daemon queue and the sync endpoint only ever hold ciphertext.
   * Null/absent = payloads go as-is (encryption disabled).
   */
  getSyncKey?: () => CryptoKey | null
}


/**
 * Seal one entry's payload for the wire: title/body under the sync key
 * (fresh IVs per attempt). Ids and timestamps stay plaintext so the daemon
 * can dedupe and the receiver can order without decrypting.
 */
async function sealEntry(entry: SyncOutboxEntry, key: CryptoKey): Promise<SyncOutboxEntry> {
  if (entry.action === 'delete' || entry.payload === null || typeof entry.payload !== 'object' || !('title' in entry.payload)) {
    return entry
  }
  const payload = entry.payload as Note
  return {
    ...entry,
    payload: {
      ...payload,
      title: JSON.stringify(await encryptBox(key, payload.title)),
      body: JSON.stringify(await encryptBox(key, payload.body)),
    },
  }
}

/** HTTP statuses that mean "this entry will NEVER succeed -- stop retrying". */
function isPermanentFailure(status: number): boolean {
  return status >= 400 && status < 500 && status !== 408 && status !== 429
}

/**
 * The sync engine behind spec §4's "Background Data Sync" scenario. Notes are
 * written locally first (optimistic UI never waits on the network) and every
 * unsynced change lands in the storage layer's durable outbox. `flush()`
 * drains the outbox via the best available path and is idempotent + safe to
 * call repeatedly: entries are only removed after a confirmed hand-off, so a
 * crash or offline flush merely leaves them queued for the next attempt.
 *
 * Hand-off idempotency: outbox entry ids are deterministic
 * (`noteId:action`), so re-enqueueing after a crash replaces instead of
 * duplicating, and each daemon hand-off carries a `batchId` the server side
 * can dedupe on. Outbox ordering uses a monotonic counter (never a bare
 * `Date.now()`, which can stall or jump backwards under NTP/domain changes).
 */
export class SyncManager {
  readonly #storage: NoteStorage
  #transport: SyncTransport | null
  readonly #fetchFn: typeof fetch
  readonly #endpoint: string | null
  readonly #onlineTarget: EventTarget | null
  readonly #getSyncKey: () => CryptoKey | null
  #chain: Promise<FlushOutcome> = Promise.resolve({ via: 'none', synced: 0, failed: 0, pending: 0 })
  #started = false
  #lastQueuedAt = 0

  constructor(deps: SyncDeps) {
    this.#storage = deps.storage
    this.#transport = deps.transport ?? null
    this.#fetchFn = deps.fetchFn ?? fetch
    this.#endpoint = deps.endpoint ?? DEFAULT_SYNC_ENDPOINT
    this.#onlineTarget = deps.onlineTarget ?? null
    this.#getSyncKey = deps.getSyncKey ?? (() => null)
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
    void this.flush().catch((error) => warnDev('sync', error))
  }

  /** Durably queue one note mutation. `delete` carries the tombstone time. */
  async queueNote(note: Note, action: SyncOutboxAction, deletedAt?: number): Promise<void> {
    const queuedAt = Math.max(Date.now(), this.#lastQueuedAt + 1)
    this.#lastQueuedAt = queuedAt
    const entry: SyncOutboxEntry =
      action === 'delete'
        ? { id: `${note.id}:delete`, kind: 'note', action: 'delete', payload: { id: note.id, deletedAt: deletedAt ?? note.updatedAt }, queuedAt }
        : { id: `${note.id}:${action}`, kind: 'note', action, payload: note, queuedAt }
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
    const stored = await this.#storage.pendingQueue()
    if (stored.length === 0) return { via: 'none', synced: 0, failed: 0, pending: 0 }
    // End-to-end sealing happens per attempt (fresh IVs): the daemon and
    // the endpoint hold ciphertext, and batch identity is unaffected --
    // batchIdFor deliberately hashes only stable fields.
    const syncKey = this.#getSyncKey()
    const entries = syncKey ? await Promise.all(stored.map((entry) => sealEntry(entry, syncKey))) : stored

    // Path A (spec §4): daemon connected -- delegate and let it own retries.
    // The PWA can be closed immediately after this resolves.
    if (this.#transport?.available) {
      try {
        await this.#transport.enqueueSyncTask(entries)
        for (const entry of stored) {
          // One at a time: a failed dequeue must not abandon its siblings.
          await this.#storage.dequeue(entry.id)
        }
        return { via: 'cortex', synced: stored.length, failed: 0, pending: 0 }
      } catch (error) {
        // Daemon accepted the connection but dropped mid-handoff: degrade.
        warnDev('sync', error)
      }
    }

    // No direct-HTTP endpoint configured: stay queued for the daemon (or a
    // future configured endpoint) rather than POSTing into the void.
    if (!this.#endpoint) {
      return { via: 'none', synced: 0, failed: 0, pending: entries.length }
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
        if (isPermanentFailure(response.status)) {
          // Dead-letter: retrying a 400-class rejection can never succeed.
          await this.#storage.deadLetter(entry, `sync endpoint returned HTTP ${response.status}`)
          await this.#storage.dequeue(entry.id)
          failed++
          continue
        }
        if (!response.ok) throw new Error(`sync endpoint returned HTTP ${response.status}`)
        await this.#storage.dequeue(entry.id)
        synced++
        try {
          await this.#markSynced(entry)
        } catch (error) {
          warnDev('sync', error) // stamped best-effort; content is already pushed
        }
      } catch (error) {
        warnDev('sync', error)
        failed++
      }
    }
    return { via: 'http', synced, failed, pending: failed }
  }

  /**
   * Stamp `syncedAt` on the stored note via one conditional storage
   * operation -- never rewrite its content. The queued payload may be stale
   * (the user may have edited the note after this entry was enqueued; a
   * newer entry then sits behind it in the outbox), so `markSyncedNote`
   * only stamps when storage still holds a revision this entry actually
   * synced; a newer stored revision is left for its own outbox entry.
   */
  async #markSynced(entry: SyncOutboxEntry): Promise<void> {
    if (entry.action === 'delete' || !('title' in entry.payload)) return
    await this.#storage.markSyncedNote(entry.payload.id, Date.now(), entry.payload.updatedAt)
  }
}
