// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { beforeEach, describe, expect, it } from 'vitest'
import { deriveAtRestKey } from '../crypto'
import { createEncryptedStorage, type AtRestKeySlot } from './encrypted'
import { MemoryStorage } from './memory'
import type { Note } from './types'

async function keyFor(pass: string): Promise<CryptoKey> {
	return deriveAtRestKey(pass, new TextEncoder().encode('test-salt'))
}

/** Read the RAW row underneath the wrapper: what the disk actually holds. */
function raw(inner: MemoryStorage): (id: string) => Promise<Note | undefined> {
	return (id: string) => inner.getNote(id)
}

describe('createEncryptedStorage', () => {
	let slot: AtRestKeySlot
	let inner: MemoryStorage
	let storage: ReturnType<typeof createEncryptedStorage>
	beforeEach(async () => {
		inner = new MemoryStorage()
		await inner.init()
		slot = { key: await keyFor('passphrase') }
		storage = createEncryptedStorage(inner, slot)
	})

	it('reads and writes transparently through the API', async () => {
		const note: Note = { id: 'n1', title: 'Secret title', body: 'Secret body', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null }
		await storage.upsertNote(note)

		expect(await storage.getNote('n1')).toEqual(note)
		expect((await storage.listNotes())[0]).toEqual(note)
	})

	it('stores ciphertext on disk, not prose', async () => {
		await storage.upsertNote({
			id: 'n1', title: 'Secret title', body: 'Secret body', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null,
		})

		const rawNote = await raw(inner)('n1')
		expect(rawNote?.title).not.toContain('Secret')
		expect(rawNote?.title).toContain('"ct"')
		expect(rawNote?.body).toContain('"iv"')
		// Metadata stays plaintext (ordering and dedup need it).
		expect(rawNote?.updatedAt).toBe(2)
	})

	it('is fail-closed: locked sessions can neither read nor write', async () => {
		await storage.upsertNote({ id: 'n1', title: 't', body: 'b', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null })

		slot.key = null
		await expect(storage.listNotes()).rejects.toThrow(/locked/)
		await expect(storage.getNote('n1')).rejects.toThrow(/locked/)
		await expect(storage.pendingQueue()).rejects.toThrow(/locked/)
		await expect(storage.upsertNote({ id: 'n2', title: 'x', body: '', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null })).rejects.toThrow(/locked/)
		// Metadata-only operations stay available while locked.
		await expect(storage.markSyncedNote('n1', 5, 2)).resolves.toBeUndefined()
		await expect(storage.deleteNote('n2')).resolves.toBeUndefined()
	})

	it('unlocks with the right key and tolerates legacy plaintext rows', async () => {
		// A row written before encryption was enabled: raw plaintext on disk.
		const legacy: Note = { id: 'old', title: 'legacy title', body: 'legacy body', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null }
		await inner.upsertNote(legacy)

		const note = await storage.getNote('old')
		expect(note).toEqual(legacy) // read path passes plaintext through

		// The next write re-encrypts it.
		await storage.upsertNote({ ...legacy, updatedAt: 3 })
		expect((await raw(inner)('old'))?.title).toContain('"ct"')
		expect((await storage.getNote('old'))?.title).toBe('legacy title')
	})

	it('encrypts outbox payloads and dead letters, not bookkeeping', async () => {
		const entry = {
			id: 'n1:create', kind: 'note' as const, action: 'create' as const,
			payload: { id: 'n1', title: 'outbox secret', body: 'body secret', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null },
			queuedAt: 9,
		}
		await storage.enqueue(entry)

		// The wrapper hands the entry back decrypted...
		const [queued] = await storage.pendingQueue()
		expect((queued.payload as Note).title).toBe('outbox secret')

		// ...but the raw outbox row holds boxes.
		const rawEntries = await inner.pendingQueue()
		expect((rawEntries[0].payload as Note).title).toContain('"ct"')

		// Dead letters get the same treatment.
		await storage.deadLetter(entry, 'HTTP 422')
		const [letter] = await storage.deadLetters()
		expect((letter.entry.payload as Note).title).toBe('outbox secret')
	})

	it('a different passphrase cannot read the rows', async () => {
		await storage.upsertNote({ id: 'n1', title: 'secret', body: '', createdAt: 1, updatedAt: 2, syncedAt: null, deletedAt: null })
		slot.key = await keyFor('different')
		await expect(storage.getNote('n1')).rejects.toThrow()
	})
})
