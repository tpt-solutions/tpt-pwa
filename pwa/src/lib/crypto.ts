// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

/**
 * Note encryption (2026-10 design decision): passphrase-derived, at rest
 * AND end-to-end in sync.
 *
 * - The user's passphrase derives AES-GCM-256 keys through WebCrypto
 *   PBKDF2-SHA256 (600k iterations — OWASP's current floor; entropy comes
 *   from the passphrase, so the fixed sync salt below is a documented
 *   tradeoff, not a weakness in strong passphrases).
 * - TWO keys from one passphrase: the AT-REST key uses a per-device random
 *   salt (data never leaves this profile) and the SYNC key uses a fixed
 *   app-wide salt so every device deriving from the same passphrase gets
 *   the same key — that is what makes end-to-end payloads decryptable
 *   without any key-distribution channel.
 * - The passphrase is never stored; keys live only for the session.
 *   A stored key would defeat the at-rest claim (anyone with the profile
 *   could decrypt), so unlock happens once per app load.
 * - Lost passphrase = unrecoverable, by design and documented.
 */

export const PBKDF2_ITERATIONS = 600_000

const AT_REST_INFO = 'tpt-pwa/at-rest/v1'
/** Fixed app-wide salt for the SYNC key: same passphrase on every device
 *  must derive the same key, so this salt travels with the code. */
const SYNC_SALT = 'tpt-pwa/sync-kdf/v1'

export type EncryptedBox = { v: 1; iv: string; ct: string }

export function randomBytes(length: number): Uint8Array<ArrayBuffer> {
	const buffer = new ArrayBuffer(length)
	crypto.getRandomValues(new Uint8Array(buffer))
	return new Uint8Array(buffer)
}

/** UTF-8 encode into an explicitly owned ArrayBuffer (TS 6's BufferSource
 *  views are particular about the backing buffer's type). */
function encode(text: string): Uint8Array<ArrayBuffer> {
	const source = new TextEncoder().encode(text)
	const buffer = new ArrayBuffer(source.length)
	const view = new Uint8Array(buffer)
	view.set(source)
	return view
}

function toBase64(bytes: Uint8Array): string {
	let binary = ''
	for (const byte of bytes) binary += String.fromCharCode(byte)
	return btoa(binary)
}

function fromBase64(text: string): Uint8Array<ArrayBuffer> {
	const binary = atob(text)
	const buffer = new ArrayBuffer(binary.length)
	const bytes = new Uint8Array(buffer)
	for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
	return bytes
}

async function deriveKey(passphrase: string, salt: BufferSource): Promise<CryptoKey> {
	const material = await crypto.subtle.importKey('raw', encode(passphrase), 'PBKDF2', false, [
		'deriveKey',
	])
	return crypto.subtle.deriveKey(
		{ name: 'PBKDF2', salt, iterations: PBKDF2_ITERATIONS, hash: 'SHA-256' },
		material,
		{ name: 'AES-GCM', length: 256 },
		false,
		['encrypt', 'decrypt'],
	)
}

/** The at-rest key: per-device random salt, never leaves this profile. */
export function deriveAtRestKey(passphrase: string, salt: BufferSource): Promise<CryptoKey> {
	return deriveKey(passphrase, salt)
}

/** The sync key: fixed app salt, so every device with the passphrase
 *  derives the same key and can decrypt each other's payloads. */
export function deriveSyncKey(passphrase: string): Promise<CryptoKey> {
	return deriveKey(passphrase, encode(SYNC_SALT))
}

/** Encrypt a UTF-8 string into a portable box ({v, iv, ct}, base64). */
export async function encryptBox(key: CryptoKey, plaintext: string): Promise<EncryptedBox> {
	const iv = randomBytes(12)
	const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, encode(plaintext))
	return { v: 1, iv: toBase64(iv), ct: toBase64(new Uint8Array(ciphertext)) }
}

/** Decrypt a box back to a UTF-8 string. A wrong key throws
 *  `OperationError` (GCM authentication failure). */
export async function decryptBox(key: CryptoKey, box: EncryptedBox): Promise<string> {
	const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: fromBase64(box.iv) }, key, fromBase64(box.ct))
	return new TextDecoder().decode(plaintext)
}

/** True when the box's shape is one of ours (cheap pre-check before
 *  attempting decryption of possibly-legacy plaintext fields). */
export function isEncryptedBox(value: unknown): value is EncryptedBox {
	return (
		typeof value === 'object' &&
		value !== null &&
		(value as EncryptedBox).v === 1 &&
		typeof (value as EncryptedBox).iv === 'string' &&
		typeof (value as EncryptedBox).ct === 'string'
	)
}

/**
 * The persisted crypto metadata: the at-rest salt and a verifier box (a
 * known plaintext encrypted under the derived key). The salt is public;
 * the verifier is how unlock checks a passphrase without any stored key.
 */
export type CryptoMeta = { salt: string; verifier: EncryptedBox }

export async function makeCryptoMeta(passphrase: string): Promise<CryptoMeta> {
	const salt = randomBytes(16)
	const key = await deriveAtRestKey(passphrase, salt)
	const verifier = await encryptBox(key, 'tpt-pwa unlock check')
	return { salt: toBase64(salt), verifier }
}

export async function checkPassphrase(meta: CryptoMeta, passphrase: string): Promise<boolean> {
	try {
		const key = await deriveAtRestKey(passphrase, fromBase64(meta.salt))
		return (await decryptBox(key, meta.verifier)) === 'tpt-pwa unlock check'
	} catch {
		return false
	}
}
