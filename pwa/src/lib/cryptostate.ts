// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { checkPassphrase, deriveAtRestKey, deriveSyncKey, makeCryptoMeta, type CryptoMeta } from './crypto'
import type { AtRestKeySlot } from './storage/encrypted'
import type { NoteStorage } from './storage'

/**
 * Session state for note encryption (see crypto.ts for the design). The
 * metadata (at-rest salt + passphrase verifier) persists in localStorage —
 * it is public by design; the derived keys exist only for the session, so
 * every app load re-asks for the passphrase. That prompt IS the at-rest
 * guarantee: a stored key would let anyone holding the browser profile
 * decrypt everything.
 */

const META_KEY = 'tpt-pwa.crypto'

function loadMeta(): CryptoMeta | null {
	try {
		const raw = localStorage.getItem(META_KEY)
		if (!raw) return null
		const parsed = JSON.parse(raw) as CryptoMeta
		return typeof parsed?.salt === 'string' && typeof parsed?.verifier === 'object' ? parsed : null
	} catch {
		return null
	}
}

function saveMeta(meta: CryptoMeta | null): void {
	try {
		if (meta === null) localStorage.removeItem(META_KEY)
		else localStorage.setItem(META_KEY, JSON.stringify(meta))
	} catch {
		/* localStorage unavailable: encryption state just does not persist */
	}
}

export const atRestSlot: AtRestKeySlot = { key: null }
let syncKey: CryptoKey | null = null
let meta: CryptoMeta | null = loadMeta()

/** Encryption was set up (metadata exists). */
export function cryptoEnabled(): boolean {
	return meta !== null
}

/** The passphrase was verified this session; storage is readable. */
export function cryptoUnlocked(): boolean {
	return meta !== null && atRestSlot.key !== null
}

/** The sync key for end-to-end payload sealing, or null when locked/disabled. */
export function getSyncKey(): CryptoKey | null {
	return syncKey
}

export function getCryptoMeta(): CryptoMeta | null {
	return meta
}

/** Verify a passphrase and fill the session key slots. */
export async function unlockCrypto(passphrase: string): Promise<boolean> {
	if (meta === null) return false
	if (!(await checkPassphrase(meta, passphrase))) return false
	atRestSlot.key = await deriveAtRestKey(passphrase, Uint8Array.from(atob(meta.salt), (c) => c.charCodeAt(0)))
	syncKey = await deriveSyncKey(passphrase)
	return true
}

/** First-time setup (or passphrase change): verify the current passphrase
 *  when encryption already exists, then persist new metadata and fill the
 *  slots. Existing rows re-encrypt on their next write. */
export async function enableCrypto(passphrase: string, previous?: string): Promise<void> {
	if (meta !== null && !(await checkPassphrase(meta, previous ?? ''))) {
		throw new Error('current passphrase is wrong')
	}
	meta = await makeCryptoMeta(passphrase)
	saveMeta(meta)
	await unlockCrypto(passphrase)
}

/**
 * Turn encryption off: verify the passphrase, rewrite every note, outbox
 * entry, and dead letter as plaintext — reading through the unlocked
 * wrapper, writing through the RAW storage (the wrapper would re-seal on
 * write) — then drop the metadata.
 */
export async function disableCrypto(passphrase: string, raw: NoteStorage, unlocked: NoteStorage): Promise<void> {
	if (meta === null) return
	if (!(await checkPassphrase(meta, passphrase))) {
		throw new Error('passphrase is wrong')
	}
	if (atRestSlot.key === null) {
		throw new Error('storage is locked')
	}
	const [notes, outbox, letters] = await Promise.all([unlocked.listNotes(), unlocked.pendingQueue(), unlocked.deadLetters()])
	for (const note of notes) await raw.upsertNote(note)
	for (const entry of outbox) await raw.enqueue(entry)
	for (const letter of letters) await raw.deadLetter(letter.entry, letter.reason)
	atRestSlot.key = null
	syncKey = null
	meta = null
	saveMeta(null)
}
