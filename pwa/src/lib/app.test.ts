// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { beforeEach, describe, expect, it } from 'vitest'
import { get } from 'svelte/store'
import { createNote, deleteNote, flushPendingSaves, updateNote, __setAppStorageForTests } from './app'
import { MemoryStorage } from './storage/memory'
import type { Note, NoteStorage, SyncOutboxEntry } from './storage'
import { notes, pendingSync } from './stores'

/** Storage double that fails selected operations, recording what succeeded. */
class FlakyStorage extends MemoryStorage {
  failUpsert = false
  failEnqueue = false
  upserts: Note[] = []

  override async upsertNote(note: Note): Promise<void> {
    if (this.failUpsert) throw new Error('disk full')
    this.upserts.push({ ...note })
    await super.upsertNote(note)
  }

  override async enqueue(entry: SyncOutboxEntry): Promise<void> {
    if (this.failEnqueue) throw new Error('daemon queue unavailable')
    await super.enqueue(entry)
  }
}

describe('app note lifecycle', () => {
  let storage: FlakyStorage
  beforeEach(async () => {
    storage = new FlakyStorage()
    __setAppStorageForTests(storage)
    notes.set([]) // the module-level stores persist across tests in this file
    pendingSync.set(0)
  })

  it('createNote: optimistic insert, then durable write and outbox entry', async () => {
    const note = await createNote('Hello', 'world')
    expect(get(notes).map((n) => n.id)).toContain(note.id)
    expect((await storage.getNote(note.id))?.title).toBe('Hello')
    expect((await storage.pendingQueue()).map((e) => e.action)).toEqual(['create'])
    expect(get(pendingSync)).toBe(1)
  })

  it('createNote rollback removes the optimistic insert AND undoes the partial storage write', async () => {
    // Storage write succeeds, then the outbox enqueue fails: the note must
    // vanish from the UI and not linger half-created in storage either.
    storage.failEnqueue = true
    await expect(createNote('Doomed')).rejects.toThrow('daemon queue unavailable')
    expect(get(notes)).toEqual([])
    expect(storage.upserts).toHaveLength(1) // it WAS written before the failure...
    expect((await storage.listNotes())).toEqual([]) // ...and was undone after
  })

  it('createNote rollback also works when the storage write itself fails', async () => {
    storage.failUpsert = true
    await expect(createNote('Doomed')).rejects.toThrow('disk full')
    expect(get(notes)).toEqual([])
  })

  it('updateNote coalesces bursts into one debounced durable write', async () => {
    const note = await createNote('v0')
    updateNote(note.id, { title: 'v1' })
    updateNote(note.id, { title: 'v2' })
    updateNote(note.id, { body: 'typed while offline' })

    await flushPendingSaves() // pagehide path: no timer wait

    const stored = await storage.getNote(note.id)
    expect(stored?.title).toBe('v2')
    expect(stored?.body).toBe('typed while offline')
    // create + exactly one coalesced update
    const actions = (await storage.pendingQueue()).map((e) => e.action).sort()
    expect(actions).toEqual(['create', 'update'])
    expect(storage.upserts.filter((n) => n.id === note.id)).toHaveLength(2)
  })

  it('a debounced edit is NOT written after the note was deleted', async () => {
    const note = await createNote('to delete')
    updateNote(note.id, { title: 'edited' })
    await deleteNote(note.id)

    await flushPendingSaves() // any lingering save timer must not resurrect the note

    const actions = (await storage.pendingQueue()).map((e) => e.action)
    expect(actions).toEqual(['create', 'delete']) // note: no 'update'
    expect(get(notes)).toEqual([])
    expect(await storage.getNote(note.id)).toBeUndefined()
  })

  it('deleteNote tombstones instead of hard-removing, so the delete can sync', async () => {
    const note = await createNote('short-lived')
    const before = Date.now()
    await deleteNote(note.id)

    const entry = (await storage.pendingQueue()).find((e) => e.action === 'delete')
    expect(entry?.payload).toMatchObject({ id: note.id })
    expect((entry!.payload as { deletedAt: number }).deletedAt).toBeGreaterThanOrEqual(before)
    expect(get(notes)).toEqual([])
    // Tombstoned notes are invisible through the normal read path...
    expect(await storage.getNote(note.id)).toBeUndefined()
    expect(await storage.listNotes()).toEqual([])
  })

  it('edits stay monotonic when the device clock jumps backwards', async () => {
    const note = await createNote('clock test')
    const storedUpdatedAt = (await storage.getNote(note.id))!.updatedAt

    const realNow = Date.now
    Date.now = () => storedUpdatedAt - 60_000 // clock went backwards a minute
    try {
      updateNote(note.id, { title: 'after clock jump' })
      await flushPendingSaves()
    } finally {
      Date.now = realNow
    }

    const stored = await storage.getNote(note.id)
    expect(stored!.updatedAt).toBeGreaterThan(storedUpdatedAt)
    expect(stored!.title).toBe('after clock jump')
  })
})

describe('markSyncedNote (atomic conditional stamp)', () => {
  it('stamps only when storage holds the revision that was actually pushed', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    const v1: Note = { id: 'n1', title: 'v1', body: '', createdAt: 1, updatedAt: 100, syncedAt: null, deletedAt: null }
    await storage.upsertNote(v1)

    // Entry for v1 pushed: stamp lands.
    await storage.markSyncedNote('n1', 555, 100)
    expect((await storage.getNote('n1'))?.syncedAt).toBe(555)

    // User edits to v2; a late stale entry (maxUpdatedAt=100) must not stamp.
    await storage.upsertNote({ ...v1, title: 'v2', updatedAt: 200, syncedAt: null })
    await storage.markSyncedNote('n1', 666, 100)
    expect((await storage.getNote('n1'))?.syncedAt).toBeNull()

    // The v2 entry can stamp.
    await storage.markSyncedNote('n1', 777, 200)
    expect((await storage.getNote('n1'))?.syncedAt).toBe(777)

    // And an already-synced note is never double-stamped.
    await storage.markSyncedNote('n1', 888, 200)
    expect((await storage.getNote('n1'))?.syncedAt).toBe(777)
  })

  it('never stamps a tombstone', async () => {
    const storage = new MemoryStorage()
    await storage.init()
    await storage.upsertNote({ id: 'n1', title: 'gone', body: '', createdAt: 1, updatedAt: 100, syncedAt: null, deletedAt: 150 })
    await storage.markSyncedNote('n1', 555, 100)
    expect(await storage.getNote('n1')).toBeUndefined()
  })
})

describe('dead letters', () => {
  it('park permanently-rejected entries with the reason, visible for inspection', async () => {
    const storage: NoteStorage = new MemoryStorage()
    await storage.init()
    const entry: SyncOutboxEntry = { id: 'n1:create', kind: 'note', action: 'create', payload: { id: 'n1', title: '', body: '', createdAt: 1, updatedAt: 1, syncedAt: null, deletedAt: null }, queuedAt: 1 }
    await storage.enqueue(entry)
    await storage.deadLetter(entry, 'sync endpoint returned HTTP 422')
    await storage.dequeue(entry.id)

    expect(await storage.pendingQueue()).toEqual([])
    const letters = await storage.deadLetters()
    expect(letters).toHaveLength(1)
    expect(letters[0].entry.id).toBe('n1:create')
    expect(letters[0].reason).toContain('422')
  })
})
