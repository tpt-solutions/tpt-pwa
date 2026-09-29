// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note } from './storage'
import { warnDev } from './devlog'

type AutomergeModule = typeof import('@automerge/automerge-wasm')
type AutomergeDoc = import('@automerge/automerge-wasm').Automerge

export type CrdtBinary = Uint8Array

let modulePromise: Promise<AutomergeModule | null> | null = null

/**
 * Load automerge-rs Wasm lazily; `null` means this attempt failed and the
 * caller proceeds without the CRDT mirror. A failed load is NOT memoized
 * (the module promise resets) so a later mutation can retry -- e.g. after a
 * transient instantiation failure -- instead of being disabled for the whole
 * session.
 */
function loadAutomerge(): Promise<AutomergeModule | null> {
  modulePromise ??= import('@automerge/automerge-wasm')
    .then(async (mod) => {
      // The bundler build exposes a wasm-bindgen `init()`; the Node build
      // instantiates eagerly. Call it only when present.
      const init = (mod as unknown as { default?: () => Promise<void> }).default
      if (typeof init === 'function') await init()
      return mod
    })
    .catch((error) => {
      warnDev('crdt', error)
      modulePromise = null // allow a retry on the next mutation
      return null
    })
  return modulePromise
}

/**
 * CRDT mirror of the note set (spec §5 "Svelte Stores + CRDTs"): automerge-rs
 * compiled to Wasm. Merges are commutative, associative and idempotent, so
 * replicas converge regardless of delivery order -- conflict resolution
 * without a central arbitrator.
 *
 * Layout: each note is a map of scalar fields, keyed by note id DIRECTLY in
 * the document root. (A dedicated `_root.notes` container would be created
 * independently by every fresh replica, and concurrent creation of that one
 * key would make merges pick a single winner -- dropping the other side's
 * notes. The root map always exists, so notes land in a pre-merged place.)
 *
 * `upsertNote` reuses a note's existing field map, so concurrent edits to
 * DIFFERENT fields merge field-by-field; concurrent edits to the same field
 * resolve last-writer-wins.
 *
 * Deletes are TOMBSTONED, not removed: `removeNote` stamps `deletedAt` into
 * the note's field map. A hard removal loses to any concurrent edit (the
 * edit's map survives the merge and resurrects the note); a tombstone is
 * just another field, so `deletedAt` wins by the same LWW rule as content
 * and `notes()` filters it out on every replica.
 *
 * Scaffold for future multi-device sync: the daemon will ferry these opaque
 * binaries between devices (cortex.task kind "crdtMerge"); local persistence
 * still lives in SQLite/IndexedDB. The UI treats this as a pure enhancement
 * and never blocks on it.
 */
export class NoteDoc {
  #doc: AutomergeDoc

  private constructor(doc: AutomergeDoc) {
    this.#doc = doc
  }

  /** Open a doc, optionally from a previously saved binary. Returns `null` when Wasm is unavailable. */
  static async open(binary?: CrdtBinary): Promise<NoteDoc | null> {
    const mod = await loadAutomerge()
    if (!mod) return null
    try {
      const doc = binary ? mod.load(binary) : mod.create()
      return new NoteDoc(doc)
    } catch (error) {
      warnDev('crdt', error)
      return null
    }
  }

  upsertNote(note: Note): void {
    const existing = this.#doc.getWithType('_root', note.id)
    const fieldsId = existing && existing[0] === 'map' ? existing[1] : this.#doc.putObject('_root', note.id, {})
    this.#doc.put(fieldsId, 'title', note.title)
    this.#doc.put(fieldsId, 'body', note.body)
    this.#doc.put(fieldsId, 'createdAt', note.createdAt)
    this.#doc.put(fieldsId, 'updatedAt', note.updatedAt)
    this.#doc.put(fieldsId, 'syncedAt', note.syncedAt)
    // Only ever WRITE tombstones: this automerge-wasm preview misbehaves
    // when `put` stores a `null` scalar (later loads of the save lose ops),
    // and a delete is final anyway -- absence of the key means live.
    if (note.deletedAt !== null) this.#doc.put(fieldsId, 'deletedAt', note.deletedAt)
  }

  /**
   * Tombstone a note in the mirror: concurrent content edits merge
   * alongside it, but `deletedAt` survives as the newest field write and
   * `notes()` filters the note out on every replica.
   */
  removeNote(id: string, deletedAt = Date.now()): void {
    const existing = this.#doc.getWithType('_root', id)
    const fieldsId =
      existing && existing[0] === 'map' ? existing[1] : this.#doc.putObject('_root', id, {})
    this.#doc.put(fieldsId, 'deletedAt', deletedAt)
  }

  /** Live (non-tombstoned) notes, newest first. */
  notes(): Note[] {
    const notes: Note[] = []
    for (const id of this.#doc.keys('_root')) {
      const existing = this.#doc.getWithType('_root', id)
      if (!existing || existing[0] !== 'map') continue
      const fieldsId = existing[1]
      const title = this.#doc.get(fieldsId, 'title')
      const body = this.#doc.get(fieldsId, 'body')
      const createdAt = this.#doc.get(fieldsId, 'createdAt')
      const updatedAt = this.#doc.get(fieldsId, 'updatedAt')
      const syncedAt = this.#doc.get(fieldsId, 'syncedAt')
      const deletedAt = this.#doc.get(fieldsId, 'deletedAt')
      if (typeof title !== 'string' || typeof body !== 'string' || typeof createdAt !== 'number' || typeof updatedAt !== 'number') continue
      if (typeof deletedAt === 'number') continue // tombstoned on some replica
      notes.push({ id, title, body, createdAt, updatedAt, syncedAt: typeof syncedAt === 'number' ? syncedAt : null, deletedAt: null })
    }
    return notes.sort((a, b) => b.updatedAt - a.updatedAt)
  }

  /** Merge a peer's document binary into this one (in place). Returns `false` if Wasm never loaded. */
  async merge(remote: CrdtBinary): Promise<boolean> {
    const mod = await loadAutomerge()
    if (!mod) return false
    let other: AutomergeDoc | null = null
    try {
      other = mod.load(remote)
      this.#doc.merge(other)
      return true
    } catch (error) {
      warnDev('crdt', error)
      return false
    } finally {
      // Free the peer doc on every path: the wasm memory backing it is
      // manual-lifetime and leaks otherwise.
      try {
        other?.free()
      } catch {
        /* already freed by a failed load/merge */
      }
    }
  }

  save(): CrdtBinary {
    return this.#doc.save()
  }

  free(): void {
    this.#doc.free()
  }
}
