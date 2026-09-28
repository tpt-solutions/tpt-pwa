// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { expect, it } from 'vitest'
import { NoteDoc } from './crdt'
import type { Note } from './storage'

/**
 * Multi-device sync demo (the "Ideas for later" item, made executable):
 * two devices diverge from a shared state while offline — one edits the
 * title, the other the body, of the SAME note — then converge with no
 * central arbitrator and no lost writes. Run it with `pnpm test`.
 *
 * In the full system the daemon ferries these opaque binaries between
 * devices (a `crdtMerge` cortex task); locally it is the same merge call.
 */
function note(id: string, title: string, body: string, updatedAt: number): Note {
  return { id, title, body, createdAt: 1000, updatedAt, syncedAt: null }
}

it('two devices editing the same note offline converge without losing either write', async () => {
  // Shared origin state, shipped to both devices.
  const origin = await NoteDoc.open()
  expect(origin).not.toBeNull()
  origin!.upsertNote(note('n1', 'Groceries', 'oat milk, rye bread', 1000))
  const baseBinary = origin!.save()

  const deviceA = await NoteDoc.open(baseBinary)
  const deviceB = await NoteDoc.open(baseBinary)
  expect(deviceA).not.toBeNull()
  expect(deviceB).not.toBeNull()

  // Both go offline and edit the same note — different fields.
  deviceA!.upsertNote(note('n1', 'Groceries (week 38)', 'oat milk, rye bread', 2000))
  deviceB!.upsertNote(note('n1', 'Groceries', 'oat milk, rye bread, coffee beans', 3000))

  // Connectivity returns: each device merges the other's binary.
  await deviceA!.merge(deviceB!.save())
  await deviceB!.merge(deviceA!.save())

  // Convergence: identical, complete state on both sides — A's title AND
  // B's body, no matter which device merges first.
  const stateA = deviceA!.notes()
  const stateB = deviceB!.notes()
  expect(stateA).toEqual(stateB)
  expect(stateA).toHaveLength(1)
  expect(stateA[0].title).toBe('Groceries (week 38)')
  expect(stateA[0].body).toBe('oat milk, rye bread, coffee beans')

  // Merge is idempotent: re-merging changes nothing.
  await deviceA!.merge(deviceB!.save())
  expect(deviceA!.notes()).toEqual(stateA)

  origin!.free()
  deviceA!.free()
  deviceB!.free()
})

it('a note created on one device simply appears on the other after merge', async () => {
  const deviceA = await NoteDoc.open()
  const deviceB = await NoteDoc.open()
  expect(deviceA).not.toBeNull()
  expect(deviceB).not.toBeNull()

  deviceA!.upsertNote(note('new-on-a', 'From device A', 'born offline', 1000))
  await deviceB!.merge(deviceA!.save())

  expect(deviceB!.notes().map((n) => n.id)).toEqual(['new-on-a'])
  // And the reverse direction carries it home too.
  await deviceA!.merge(deviceB!.save())
  expect(deviceA!.notes().map((n) => n.id)).toEqual(['new-on-a'])

  deviceA!.free()
  deviceB!.free()
})
