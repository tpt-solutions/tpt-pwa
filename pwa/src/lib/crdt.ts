// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note } from './storage'

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
    .catch(() => null)
  return modulePromise
}

/**
 * CRDT mirror of the note set (spec §5 "Svelte Stores + CRDTs"): automerge-rs
 * compiled to Wasm. Merges are commutative, associative and idempotent, so
 * replicas converge regardless of delivery order -- conflict resolution
 * without a central arbitrator.
 *
 * Scaffold for future multi-device sync: the daemon will ferry these opaque
 * binaries between devices (cortex.task kind "crdtMerge"); local persistence
 * still lives in SQLite/IndexedDB. The UI treats this as a pure enhancement
 * and never blocks on it.
 */
export class NoteDoc {
  #doc: AutomergeDoc
  #notesId: string

  private constructor(doc: AutomergeDoc, notesId: string) {
    this.#doc = doc
    this.#notesId = notesId
  }

  /** Open a doc, optionally from a previously saved binary. Returns `null` when Wasm is unavailable. */
  static async open(binary?: CrdtBinary): Promise<NoteDoc | null> {
    const mod = await loadAutomerge()
    if (!mod) return null
    try {
      const doc = binary ? mod.load(binary) : mod.create()
      const existing = doc.getWithType('_root', 'notes')
      const notesId = existing && existing[0] === 'map' ? existing[1] : doc.putObject('_root', 'notes', {})
      return new NoteDoc(doc, notesId)
    } catch {
      return null
    }
  }

  upsertNote(note: Note): void {
    const fieldsId = this.#doc.putObject(this.#notesId, note.id, {})
    this.#doc.put(fieldsId, 'title', note.title)
    this.#doc.put(fieldsId, 'body', note.body)
    this.#doc.put(fieldsId, 'createdAt', note.createdAt)
    this.#doc.put(fieldsId, 'updatedAt', note.updatedAt)
    this.#doc.put(fieldsId, 'syncedAt', note.syncedAt)
  }

  removeNote(id: string): void {
    this.#doc.delete(this.#notesId, id)
  }

  /** Snapshot of every note currently in the CRDT, newest first. */
  notes(): Note[] {
    const notes: Note[] = []
    for (const id of this.#doc.keys(this.#notesId)) {
      const fieldsId = this.#doc.get(this.#notesId, id)
      if (typeof fieldsId !== 'string') continue
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
    } catch {
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
