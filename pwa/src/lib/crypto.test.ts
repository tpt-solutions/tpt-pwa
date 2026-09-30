// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import {
	checkPassphrase,
	decryptBox,
	deriveAtRestKey,
	deriveSyncKey,
	encryptBox,
	isEncryptedBox,
	makeCryptoMeta,
	randomBytes,
} from './crypto'

describe('boxes', () => {
	it('round-trips a string and never repeats an IV', async () => {
		const key = await deriveSyncKey('correct horse battery staple')
		const box = await encryptBox(key, 'hello 世界')
		expect(isEncryptedBox(box)).toBe(true)
		expect(box.ct).not.toContain('hello')
		expect(await decryptBox(key, box)).toBe('hello 世界')

		// Fresh IVs: encrypting twice yields different ciphertexts.
		const again = await encryptBox(key, 'hello 世界')
		expect(again.iv).not.toBe(box.iv)
		expect(again.ct).not.toBe(box.ct)
	})

	it('fails closed on a wrong key', async () => {
		const key = await deriveSyncKey('right')
		const wrong = await deriveSyncKey('wrong')
		const box = await encryptBox(key, 'secret')
		await expect(decryptBox(wrong, box)).rejects.toThrow()
	})

	it('isEncryptedBox rejects plaintext and junk', () => {
		expect(isEncryptedBox('plain text')).toBe(false)
		expect(isEncryptedBox({ v: 1, iv: 'x' })).toBe(false)
		expect(isEncryptedBox(null)).toBe(false)
	})
})

describe('key derivation', () => {
	it('is deterministic per salt and separated across purposes', async () => {
		const salt = randomBytes(16)
		const a1 = await deriveAtRestKey('pass', salt)
		const a2 = await deriveAtRestKey('pass', salt)
		const box = await encryptBox(a1, 'x')
		await expect(decryptBox(a2, box)).resolves.toBe('x') // same pass+salt -> same key

		const otherSalt = await deriveAtRestKey('pass', randomBytes(16))
		await expect(decryptBox(otherSalt, box)).rejects.toThrow() // different salt -> different key

		// The sync key equals the at-rest key only for the fixed sync salt:
		// deriveSyncKey('p') must decrypt what another deriveSyncKey('p') made.
		const s1 = await deriveSyncKey('passphrase')
		const s2 = await deriveSyncKey('passphrase')
		const syncBox = await encryptBox(s1, 'payload')
		await expect(decryptBox(s2, syncBox)).resolves.toBe('payload')
		await expect(decryptBox(await deriveAtRestKey('passphrase', salt), syncBox)).rejects.toThrow()
	})
})

describe('crypto meta (unlock verifier)', () => {
	it('accepts the right passphrase and rejects the wrong one', async () => {
		const meta = await makeCryptoMeta('my passphrase')
		expect(meta.salt).toBeTruthy()
		expect(await checkPassphrase(meta, 'my passphrase')).toBe(true)
		expect(await checkPassphrase(meta, 'wrong')).toBe(false)
		// A fresh meta uses a fresh salt: same passphrase, different verifier.
		const meta2 = await makeCryptoMeta('my passphrase')
		expect(meta2.salt).not.toBe(meta.salt)
	})
})
