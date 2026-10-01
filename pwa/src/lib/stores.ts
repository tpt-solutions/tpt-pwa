// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { writable } from 'svelte/store'
import type { Note, NoteStorage } from './storage'
import type { FlushOutcome } from './sync'
import type { TaskSummary } from './telemetry'

export type AppStatus = 'loading' | 'locked' | 'ready' | 'error'

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

/** Note-encryption state for the UI (see cryptostate.ts / crypto.ts). */
export const cryptoStatus = writable<{ enabled: boolean; unlocked: boolean }>({ enabled: false, unlocked: false })
export const capabilities = writable<Capabilities>({ cortex: false, storageBackend: null, crdt: false })

/** Newest-first note list; mutations are optimistic and rolled back on persistence failure. */
export const notes = writable<Note[]>([])

/** The saved .ctx script library (CRDT-backed; empty while the mirror is off). */
export type StoredScript = { id: string; name: string; source: string; createdAt: number; updatedAt: number }
export const scripts = writable<StoredScript[]>([])

/** Entries currently waiting in the durable outbox. */
export const pendingSync = writable<number>(0)

/** Browser connectivity mirror for UI hints; the source of truth for fallback scheduling is the `online` event. */
export const online = writable(true)

/** Captured `beforeinstallprompt` event, or null once installed / unsupported. */
export type InstallPrompt = { prompt: () => Promise<void> }
export const installPrompt = writable<InstallPrompt | null>(null)

/** Telemetry for the status panel: daemon identity and its queue's shape. */
export const daemonVersion = writable<string | null>(null)
export const daemonTasks = writable<TaskSummary | null>(null)

/** Result of the most recent outbox flush (null = none this session). */
export const lastFlush = writable<FlushOutcome | null>(null)

/** A new service worker is waiting; the UI offers "reload to update". */
export const swUpdateReady = writable(false)
