// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { writable } from 'svelte/store'
import type { Note, NoteStorage } from './storage'

export type AppStatus = 'loading' | 'ready' | 'error'

/** The negotiated execution environment, surfaced for the UI's status chips. */
export type Capabilities = {
  /** tpt-cortex daemon reachable over the loopback WebSocket (spec §4 Path A). */
  cortex: boolean
  /** Which storage engine won capability negotiation. */
  storageBackend: NoteStorage['backend'] | null
  /** automerge Wasm available for the CRDT sync scaffold (spec §5). */
  crdt: boolean
}

export const appStatus = writable<AppStatus>('loading')
export const capabilities = writable<Capabilities>({ cortex: false, storageBackend: null, crdt: false })

/** Newest-first note list; mutations are optimistic and rolled back on persistence failure. */
export const notes = writable<Note[]>([])

/** Entries currently waiting in the durable outbox. */
export const pendingSync = writable<number>(0)

/** Browser connectivity mirror for UI hints; the source of truth for fallback scheduling is the `online` event. */
export const online = writable(true)

/** Captured `beforeinstallprompt` event, or null once installed / unsupported. */
export type InstallPrompt = { prompt: () => Promise<void> }
export const installPrompt = writable<InstallPrompt | null>(null)
