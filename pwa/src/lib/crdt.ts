// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note } from './storage'
import { warnDev } from './devlog'

type AutomergeModule = typeof import('@automerge/automerge-wasm')
type AutomergeDoc = import('@automerge/automerge-wasm').Automerge

export type CrdtBinary = Uint8Array

let modulePromise: Promise<AutomergeModule | null> | null = null

/** Load automerge-rs Wasm lazily; `null` means the environment can't run it and the app proceeds without the CRDT mirror. */
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
  }

  removeNote(id: string): void {
    this.#doc.delete('_root', id)
  }

  /** Snapshot of every note currently in the CRDT, newest first. */
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
      if (typeof title !== 'string' || typeof body !== 'string' || typeof createdAt !== 'number' || typeof updatedAt !== 'number') continue
      notes.push({ id, title, body, createdAt, updatedAt, syncedAt: typeof syncedAt === 'number' ? syncedAt : null })
    }
    return notes.sort((a, b) => b.updatedAt - a.updatedAt)
  }

  /** Merge a peer's document binary into this one (in place). Returns `false` if Wasm never loaded. */
  async merge(remote: CrdtBinary): Promise<boolean> {
    const mod = await loadAutomerge()
    if (!mod) return false
    try {
      const other = mod.load(remote)
      this.#doc.merge(other)
      other.free()
      return true
    } catch (error) {
      warnDev('crdt', error)
      return false
    }
  }

  save(): CrdtBinary {
    return this.#doc.save()
  }

  free(): void {
    this.#doc.free()
  }
}
