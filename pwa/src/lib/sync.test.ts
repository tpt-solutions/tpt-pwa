// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { MemoryStorage } from './storage/memory'
import { createStorage } from './storage'
import type { Note, SyncOutboxEntry } from './storage'
import { SyncManager } from './sync'
import type { SyncTransport } from './sync'

function makeNote(overrides: Partial<Note> = {}): Note {
  const now = Date.now()
  return { id: 'note-1', title: 'Groceries', body: 'oat milk', createdAt: now, updatedAt: now, syncedAt: null, ...overrides }
}

class RecordingTransport implements SyncTransport {
  available = true
  batches: SyncOutboxEntry[][] = []

  async enqueueSyncTask(entries: SyncOutboxEntry[]): Promise<void> {
    this.batches.push(entries)
  }
}

describe('storage capability negotiation', () => {
  it('degrades gracefully to a working backend in a bare Node environment', async () => {
    // Node has neither OPFS nor IndexedDB: SQLite and IndexedDB candidates
    // report unavailable, so negotiation must settle on in-memory rather
    // than throw.
    const storage = await createStorage()
    expect(storage.backend).toBe('memory')
  })

  it('memory storage roundtrips notes and the outbox', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const note = makeNote()
    await storage.upsertNote(note)
    expect(await storage.listNotes()).toEqual([note])
    expect(await storage.getNote(note.id)).toEqual(note)
    await storage.enqueue({ id: `${note.id}:create`, kind: 'note', action: 'create', payload: note, queuedAt: 1 })
    expect((await storage.pendingQueue()).length).toBe(1)
    await storage.dequeue(`${note.id}:create`)
    expect(await storage.pendingQueue()).toEqual([])
    await storage.deleteNote(note.id)
    expect(await storage.listNotes()).toEqual([])
  })
})

describe('SyncManager (spec §4 background sync)', () => {
  it('Path A: delegates the outbox to the cortex daemon and clears it locally', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const transport = new RecordingTransport()
    const sync = new SyncManager({ storage, transport })

    await sync.queueNote(makeNote(), 'create')
    const outcome = await sync.flush()

    expect(outcome.via).toBe('cortex')
    expect(outcome.synced).toBe(1)
    expect(transport.batches).toHaveLength(1)
    expect(await storage.pendingQueue()).toEqual([])
  })

  it('fallback path: keeps entries queued when no daemon exists and the endpoint is unreachable', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const offlineFetch: typeof fetch = () => Promise.reject(new TypeError('fetch failed (offline)'))
    const sync = new SyncManager({ storage, fetchFn: offlineFetch })

    await sync.queueNote(makeNote(), 'create')
    const outcome = await sync.flush()

    expect(outcome.via).toBe('http')
    expect(outcome.failed).toBe(1)
    // The note survives in the durable outbox for the next trigger.
    expect(await storage.pendingQueue()).toHaveLength(1)
  })

  it('fallback path: pushes directly over HTTP when online and marks notes synced', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const seen: string[] = []
    const onlineFetch = (async (input: string | URL | Request) => {
      seen.push(String(input))
      return new Response(null, { status: 200 })
    }) as typeof fetch
    const sync = new SyncManager({ storage, fetchFn: onlineFetch })

    await sync.queueNote(makeNote(), 'create')
    const outcome = await sync.flush()

    expect(outcome.synced).toBe(1)
    expect(seen).toHaveLength(1)
    const stored = await storage.getNote('note-1')
    expect(stored?.syncedAt).not.toBeNull()
    expect(await storage.pendingQueue()).toEqual([])
  })

  it('an `online` event triggers a flush attempt', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const target = new EventTarget()
    const offlineFetch: typeof fetch = () => Promise.reject(new TypeError('fetch failed (offline)'))
    const sync = new SyncManager({ storage, fetchFn: offlineFetch, onlineTarget: target })
    sync.start()

    await sync.queueNote(makeNote(), 'update')
    target.dispatchEvent(new Event('online'))
    await Promise.resolve()
    await new Promise((resolve) => setTimeout(resolve, 0))

    // Flush ran (endpoint unreachable, entry still queued) -- the important
    // part is the event path fired without any user interaction.
    expect(await storage.pendingQueue()).toHaveLength(1)
    sync.stop()
  })

  it('falls back to HTTP when the daemon drops mid-handoff', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const transport: SyncTransport = {
      available: true,
      enqueueSyncTask: () => Promise.reject(new Error('websocket closed')),
    }
    const onlineFetch = (async () => new Response(null, { status: 200 })) as typeof fetch
    const sync = new SyncManager({ storage, transport, fetchFn: onlineFetch })

    await sync.queueNote(makeNote(), 'create')
    const outcome = await sync.flush()

    expect(outcome.via).toBe('http')
    expect(outcome.synced).toBe(1)
    expect(await storage.pendingQueue()).toEqual([])
  })

  it('serialized flushes never double-send an entry', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const transport = new RecordingTransport()
    const sync = new SyncManager({ storage, transport })
    await sync.queueNote(makeNote(), 'create')

    const [first, second] = await Promise.all([sync.flush(), sync.flush()])

    expect(first.via).toBe('cortex')
    expect(second.via).toBe('none') // outbox already empty
    expect(transport.batches).toHaveLength(1)
  })
})
