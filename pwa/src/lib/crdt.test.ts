// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { NoteDoc } from './crdt'
import type { Note } from './storage'

function note(id: string, title: string, updatedAt = 1000): Note {
  return { id, title, body: `body of ${id}`, createdAt: 1000, updatedAt, syncedAt: null, deletedAt: null }
}

it('roundtrips a document through the binary format', async () => {
  const doc = await NoteDoc.open()
  expect(doc).not.toBeNull()
  if (!doc) return

  doc.upsertNote(note('a', 'First'))
  doc.upsertNote(note('b', 'Second', 2000))

  const restored = await NoteDoc.open(doc.save())
  expect(restored?.notes()).toEqual(doc.notes())
  expect(restored?.notes().map((n) => n.id)).toEqual(['b', 'a'])

  doc.free()
  restored?.free()
})

it('merges concurrent replicas without conflicts', async () => {
  const base = await NoteDoc.open()
  expect(base).not.toBeNull()
  if (!base) return

  base.upsertNote(note('shared', 'original'))

  // Two devices diverge from the same state.
  const deviceA = await NoteDoc.open(base.save())
  const deviceB = await NoteDoc.open(base.save())
  deviceA?.upsertNote(note('from-a', 'written on A', 2000))
  deviceB?.upsertNote(note('from-b', 'written on B', 3000))

  // Order of merge must not matter (commutativity).
  await deviceA?.merge(deviceB!.save())
  await deviceB?.merge(deviceA!.save())

  expect(deviceA?.notes().map((n) => n.id).sort()).toEqual(['from-a', 'from-b', 'shared'])
  expect(deviceB?.notes()).toEqual(deviceA?.notes())

  base.free()
  deviceA?.free()
  deviceB?.free()
})

it('removes notes and keeps removals across a merge roundtrip', async () => {
  const doc = await NoteDoc.open()
  if (!doc) return
  doc.upsertNote(note('a', 'First'))
  doc.upsertNote(note('b', 'Second'))
  doc.removeNote('a')
  expect(doc.notes().map((n) => n.id)).toEqual(['b'])

  const restored = await NoteDoc.open(doc.save())
  expect(restored?.notes().map((n) => n.id)).toEqual(['b'])

  doc.free()
  restored?.free()
})

it('a tombstoned delete wins over a concurrent edit on another replica', async () => {
  const origin = await NoteDoc.open()
  if (!origin) return
  origin.upsertNote(note('n1', 'original'))
  const baseBinary = origin.save()

  // Device B deletes while device A edits the same note offline.
  const deviceA = await NoteDoc.open(baseBinary)
  const deviceB = await NoteDoc.open(baseBinary)
  if (!deviceA || !deviceB) return
  deviceA.upsertNote(note('n1', 'edited on A', 2000))
  deviceB.removeNote('n1', 3000)

  // Both merge orders must agree: the note stays deleted (the tombstone is
  // the newest field write, so a concurrent content edit cannot resurrect
  // it), on every replica.
  await deviceA.merge(deviceB.save())
  expect(deviceA.notes().map((n) => n.id)).toEqual([])
  await deviceB.merge(deviceA.save())
  expect(deviceB.notes().map((n) => n.id)).toEqual([])

  origin.free()
  deviceA.free()
  deviceB.free()
})

describe('.ctx script library in the CRDT doc', () => {
  const script = (id: string, name: string, source: string, updatedAt: number) => ({
    id,
    name,
    source,
    createdAt: updatedAt - 1,
    updatedAt,
  })

  it('round-trips scripts and updates in place', async () => {
    const doc = await NoteDoc.open()
    if (!doc) return
    doc.upsertScript(script('s1', 'fetch feed', 'task f() -> void { }', 100))
    doc.upsertScript(script('s2', 'push notes', 'task p() -> void { }', 200))
    expect(doc.scripts().map((s) => s.id)).toEqual(['s2', 's1'])

    doc.upsertScript(script('s1', 'fetch feed v2', 'task f2() -> void { }', 300))
    const list = doc.scripts()
    expect(list).toHaveLength(2)
    expect(list[0]).toEqual(script('s1', 'fetch feed v2', 'task f2() -> void { }', 300))
  })

  it('a delete tombstone is sticky: a concurrent edit cannot resurrect', async () => {
    // Same guarantee as notes (see the note tombstone-merge test): once a
    // script is tombstoned in the mirror, later upserts of the same id keep
    // it dead -- deletes never lose to edits. A library entry that is
    // genuinely wanted again gets re-saved under a new id.
    const doc = await NoteDoc.open()
    if (!doc) return
    doc.upsertScript(script('s1', 'shared', 'task s() -> void { }', 100))
    const baseBinary = doc.save()

    // Device B deletes while device A renames the same script offline.
    const deviceA = await NoteDoc.open(baseBinary)
    const deviceB = await NoteDoc.open(baseBinary)
    if (!deviceA || !deviceB) return
    deviceB.removeScript('s1')
    deviceA.upsertScript(script('s1', 'renamed on A', 'task s() -> void { }', 300))

    await deviceA.merge(deviceB.save())
    await deviceB.merge(deviceA.save())
    expect(deviceA.scripts()).toEqual([])
    expect(deviceB.scripts()).toEqual([])

    // Editing the tombstoned id afterwards does not resurrect it either.
    deviceA.upsertScript(script('s1', 'zombie', 'task s() -> void { }', 999))
    expect(deviceA.scripts()).toEqual([])

    doc.free()
    deviceA.free()
    deviceB.free()
  })

  it('field-level edits to one script merge without losing the other side', async () => {
    const doc = await NoteDoc.open()
    if (!doc) return
    doc.upsertScript(script('s1', 'shared', 'task s() -> void { }', 100))
    const baseBinary = doc.save()

    const deviceA = await NoteDoc.open(baseBinary)
    const deviceB = await NoteDoc.open(baseBinary)
    if (!deviceA || !deviceB) return
    deviceA.upsertScript(script('s1', 'renamed on A', 'task s() -> void { }', 300))
    deviceB.upsertScript(script('s1', 'shared', 'task edited on B() -> void { }', 300))

    await deviceA.merge(deviceB.save())
    await deviceB.merge(deviceA.save())
    // Concurrent edits to DIFFERENT fields (name on A, source on B) merge
    // field-by-field.
    const merged = deviceA.scripts()
    expect(merged).toHaveLength(1)
    expect(merged[0].name).toBe('renamed on A')
    expect(merged[0].source).toBe('task edited on B() -> void { }')
    expect(deviceB.scripts()[0]).toEqual(merged[0])

    doc.free()
    deviceA.free()
    deviceB.free()
  })

  it('scripts and notes coexist in one document without colliding', async () => {
    const doc = await NoteDoc.open()
    if (!doc) return
    doc.upsertNote(note('n1', 'a note', 100))
    doc.upsertScript(script('n1', 'same id as the note', 'task s() -> void { }', 200))
    expect(doc.notes().map((n) => n.id)).toEqual(['n1'])
    expect(doc.scripts().map((s) => s.id)).toEqual(['n1'])
    const reopened = await NoteDoc.open(doc.save())
    if (!reopened) return
    expect(reopened.scripts()).toHaveLength(1)
    expect(reopened.notes()).toHaveLength(1)
    doc.free()
    reopened.free()
  })
})
