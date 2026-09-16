// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { expect, it } from 'vitest'
import { NoteDoc } from './crdt'
import type { Note } from './storage'

function note(id: string, title: string, updatedAt = 1000): Note {
  return { id, title, body: `body of ${id}`, createdAt: 1000, updatedAt, syncedAt: null }
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
