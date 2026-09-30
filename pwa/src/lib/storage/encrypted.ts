// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { decryptBox, encryptBox, isEncryptedBox, type EncryptedBox } from '../crypto'
import type { DeadLetter, Note, NoteStorage, SyncOutboxEntry } from './types'

/**
 * At-rest encryption (2026-10 design decision): wraps whichever backend won
 * capability negotiation and encrypts every piece of note CONTENT --
 * `title` and `body` on note rows, and the same fields inside outbox and
 * dead-letter payloads -- under the passphrase-derived at-rest key. IDs and
 * timestamps stay plaintext (ordering, LWW and dedup need them); only what
 * a stolen disk could read as prose is protected.
 *
 * The wrapper is fail-closed: with no key in the session slot (app locked)
 * every read or write throws, and the app gates itself behind the lock
 * screen before touching storage. Reads tolerate legacy plaintext (data
 * written before encryption was enabled), so enabling encryption can be
 * lazy -- rows re-encrypt on their next write.
 */

export type AtRestKeySlot = { key: CryptoKey | null }

class LockedError extends Error {
	constructor() {
		super('encrypted storage is locked')
		this.name = 'LockedError'
	}
}

export function createEncryptedStorage(inner: NoteStorage, slot: AtRestKeySlot): NoteStorage {
	function requireKey(): CryptoKey {
		if (slot.key === null) throw new LockedError()
		return slot.key
	}

	async function seal(text: string): Promise<string> {
		return JSON.stringify(await encryptBox(requireKey(), text))
	}

	async function unseal(text: string): Promise<string> {
		let parsed: unknown
		try {
			parsed = JSON.parse(text)
		} catch {
			return text // legacy plaintext, written before encryption
		}
		if (!isEncryptedBox(parsed)) return text
		return decryptBox(requireKey(), parsed as EncryptedBox)
	}

	async function unsealNote(note: Note): Promise<Note> {
		return { ...note, title: await unseal(note.title), body: await unseal(note.body) }
	}

	async function sealEntry(entry: SyncOutboxEntry): Promise<SyncOutboxEntry> {
		if (entry.action === 'delete' || entry.payload === null || typeof entry.payload !== 'object' || !('title' in entry.payload)) {
			return entry
		}
		const payload = entry.payload as Note
		return {
			...entry,
			payload: {
				...payload,
				title: await seal(payload.title),
				body: await seal(payload.body),
			},
		}
	}

	async function unsealEntry(entry: SyncOutboxEntry): Promise<SyncOutboxEntry> {
		if (entry.action === 'delete' || entry.payload === null || typeof entry.payload !== 'object' || !('title' in entry.payload)) {
			return entry
		}
		return { ...entry, payload: await unsealNote(entry.payload as Note) }
	}

	return {
		backend: inner.backend,
		get available() {
			return inner.available
		},
		init: () => inner.init(),

		async listNotes() {
			requireKey()
			const notes = await inner.listNotes()
			const out: Note[] = []
			for (const note of notes) out.push(await unsealNote(note))
			return out
		},
		async getNote(id) {
			requireKey()
			const note = await inner.getNote(id)
			return note ? unsealNote(note) : undefined
		},
		async upsertNote(note) {
			requireKey()
			await inner.upsertNote({ ...note, title: await seal(note.title), body: await seal(note.body) })
		},
		deleteNote: (id) => inner.deleteNote(id),
		markSyncedNote: (id, syncedAt, maxUpdatedAt) => inner.markSyncedNote(id, syncedAt, maxUpdatedAt),

		async enqueue(entry) {
			requireKey()
			await inner.enqueue(await sealEntry(entry))
		},
		dequeue: (id) => inner.dequeue(id),
		async pendingQueue() {
			requireKey()
			const entries = await inner.pendingQueue()
			const out: SyncOutboxEntry[] = []
			for (const entry of entries) out.push(await unsealEntry(entry))
			return out
		},

		async deadLetter(entry, reason) {
			requireKey()
			await inner.deadLetter(await sealEntry(entry), reason)
		},
		async deadLetters() {
			requireKey()
			const letters: DeadLetter[] = await inner.deadLetters()
			return Promise.all(letters.map(async (letter) => ({ ...letter, entry: await unsealEntry(letter.entry) })))
		},
	}
}
