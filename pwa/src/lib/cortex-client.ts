// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { SyncOutboxEntry } from './storage'
import type { SyncTransport } from './sync'

/** Status of the WebSocket link to the tpt-cortex daemon (spec §3 Layer 2). */
export type CortexStatus = 'disconnected' | 'connecting' | 'connected'

/** The daemon's fixed local endpoint (spec §3): loopback only, no TLS needed on localhost. */
export const CORTEX_WS_URL = 'ws://127.0.0.1:9911/rpc'

/**
 * Shared token for /rpc, when the daemon enforces one (the Android companion
 * always does — see docs/jsonrpc-contract.md). Delivered by the companion via
 * the `cortex:auth` DOM event; appended to every connect attempt.
 */
let cortexAuthToken: string | null = null

export function setCortexAuthToken(token: string): void {
  cortexAuthToken = token
}

/** The URL to dial, including the auth token when one is known. */
export function cortexWSURL(url: string = CORTEX_WS_URL): string {
  if (!cortexAuthToken) return url
  return `${url}${url.includes('?') ? '&' : '?'}token=${encodeURIComponent(cortexAuthToken)}`
}

const RECONNECT_BASE_MS = 500
const RECONNECT_MAX_MS = 30_000

export class CortexUnavailableError extends Error {
  constructor() {
    super('tpt-cortex daemon is not connected')
    this.name = 'CortexUnavailableError'
  }
}

type PendingCall = {
  resolve: (value: unknown) => void
  reject: (error: Error) => void
  timer: ReturnType<typeof setTimeout>
}

type WebSocketLike = {
  send(data: string): void
  close(): void
  addEventListener(type: string, listener: (event: { data?: unknown }) => void): void
}

const defaultWsFactory = (url: string) => new WebSocket(url)

/**
 * JSON-RPC 2.0 client over the loopback WebSocket to cortex-daemon. The wire
 * contract is documented in docs/jsonrpc-contract.md and shared with the Go
 * daemon and the Android companion -- one contract, three runtimes.
 *
 * Once a handshake has succeeded (and auto-reconnect is enabled), the client
 * re-connects itself with capped exponential backoff when the daemon drops;
 * in-flight calls are rejected on close so callers never hang behind a dead
 * socket.
 */
export class CortexRPC {
  #ws: WebSocket | null = null
  #pending = new Map<number, PendingCall>()
  #nextId = 1
  #statusListeners = new Set<(status: CortexStatus) => void>()
  #notificationListeners = new Map<string, Set<(params: unknown) => void>>()
  #connectPromise: Promise<boolean> | null = null
  #reconnectTimer: ReturnType<typeof setTimeout> | null = null
  #reconnectAttempt = 0
  #autoReconnect = false
  #closedByUser = false
  #wsFactory: (url: string) => WebSocketLike

  constructor(wsFactory?: (url: string) => WebSocketLike) {
    this.#wsFactory = wsFactory ?? defaultWsFactory
  }

  status: CortexStatus = 'disconnected'

  get connected(): boolean {
    return this.status === 'connected'
  }

  onStatus(listener: (status: CortexStatus) => void): () => void {
    this.#statusListeners.add(listener)
    return () => this.#statusListeners.delete(listener)
  }

  /** Subscribe to server-initiated notifications (e.g. `cortex.event.taskCompleted`). */
  onNotification(method: string, listener: (params: unknown) => void): () => void {
    let set = this.#notificationListeners.get(method)
    if (!set) {
      set = new Set()
      this.#notificationListeners.set(method, set)
    }
    set.add(listener)
    return () => set.delete(listener)
  }

  /**
   * Feature-detect the daemon (spec §3 `hasCortex`): resolve `true` only if a
   * real handshake completes within `timeoutMs`. Never throws -- absence of
   * the daemon is a normal state, not an error. Concurrent callers share one
   * in-flight attempt instead of opening competing sockets.
   */
  connect(url: string = cortexWSURL(), timeoutMs = 2000): Promise<boolean> {
    if (this.connected) return Promise.resolve(true)
    // No WebSocket implementation (tests, old JS engines) and none injected:
    // absence of the daemon API is a normal "not connected".
    if (typeof WebSocket === 'undefined' && this.#wsFactory === defaultWsFactory) return Promise.resolve(false)
    this.#connectPromise ??= this.#connectOnce(url, timeoutMs).finally(() => {
      this.#connectPromise = null
    })
    return this.#connectPromise
  }

  /**
   * Keep the link alive across daemon restarts: after the first successful
   * handshake, drops re-connect with capped exponential backoff. Probing a
   * daemon that was never there does not schedule retries.
   */
  enableAutoReconnect(): void {
    this.#autoReconnect = true
  }

  #connectOnce(url: string, timeoutMs: number): Promise<boolean> {
    this.#setStatus('connecting')
    return new Promise((resolve) => {
      let settled = false
      let ws: WebSocketLike
      try {
        ws = this.#wsFactory(url)
      } catch {
        this.#setStatus('disconnected')
        return resolve(false)
      }
      const finish = (connected: boolean) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        if (!connected) {
          try {
            ws.close()
          } catch {
            /* never opened */
          }
          if (this.#ws === ws) this.#ws = null
          this.#setStatus('disconnected')
        }
        resolve(connected)
      }
      const timer = setTimeout(() => finish(false), timeoutMs)
      ws.addEventListener('open', () => {
        this.#ws = ws as WebSocket
        this.#setStatus('connected')
        this.#reconnectAttempt = 0
        finish(true)
      })
      ws.addEventListener('message', (event) => this.#dispatch(String((event as MessageEvent).data)))
      ws.addEventListener('close', () => {
        if (this.#ws === ws) {
          this.#ws = null
          this.#setStatus('disconnected')
        }
        this.#rejectAllPending()
        this.#scheduleReconnect()
        finish(false)
      })
      ws.addEventListener('error', () => finish(false))
    })
  }

  #rejectAllPending(): void {
    for (const [id, pending] of this.#pending) {
      clearTimeout(pending.timer)
      pending.reject(new CortexUnavailableError())
      this.#pending.delete(id)
    }
  }

  #scheduleReconnect(): void {
    if (!this.#autoReconnect || this.#closedByUser || this.#reconnectTimer !== null) return
    const delay = Math.min(RECONNECT_BASE_MS * 2 ** this.#reconnectAttempt, RECONNECT_MAX_MS)
    this.#reconnectAttempt++
    this.#reconnectTimer = setTimeout(() => {
      this.#reconnectTimer = null
      void this.connect().then((connected) => {
        if (!connected && this.#autoReconnect && !this.#closedByUser) this.#scheduleReconnect()
      })
    }, delay)
  }

  /** Fire a JSON-RPC request and await its response; rejects on timeout or daemon error. */
  call<T = unknown>(method: string, params?: unknown, timeoutMs = 8000): Promise<T> {
    const ws = this.#ws
    if (!this.connected || !ws) return Promise.reject(new CortexUnavailableError())
    const id = this.#nextId++
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id)
        reject(new Error(`cortex RPC "${method}" timed out after ${timeoutMs}ms`))
      }, timeoutMs)
      this.#pending.set(id, {
        resolve: (value) => resolve(value as T),
        reject,
        timer,
      })
      ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }))
    })
  }

  /** Fire-and-forget JSON-RPC notification (no id, no response). */
  notify(method: string, params?: unknown): void {
    const ws = this.#ws
    if (!this.connected || !ws) throw new CortexUnavailableError()
    ws.send(JSON.stringify({ jsonrpc: '2.0', method, params }))
  }

  close(): void {
    this.#closedByUser = true
    if (this.#reconnectTimer !== null) {
      clearTimeout(this.#reconnectTimer)
      this.#reconnectTimer = null
    }
    this.#ws?.close()
    this.#ws = null
    this.#setStatus('disconnected')
  }

  #setStatus(status: CortexStatus): void {
    this.status = status
    for (const listener of this.#statusListeners) listener(status)
  }

  #dispatch(data: string): void {
    let message: unknown
    try {
      message = JSON.parse(data)
    } catch {
      return // not JSON: ignore rather than trust arbitrary bytes
    }
    if (typeof message !== 'object' || message === null) return
    const { id, method, params, result, error } = message as Record<string, unknown>
    if (typeof id === 'number' && this.#pending.has(id)) {
      const pending = this.#pending.get(id)!
      clearTimeout(pending.timer)
      this.#pending.delete(id)
      if (error !== undefined) {
        const messageText = typeof (error as { message?: unknown })?.message === 'string' ? (error as { message: string }).message : 'cortex RPC error'
        pending.reject(new Error(messageText))
      } else {
        pending.resolve(result)
      }
      return
    }
    if (typeof method === 'string') {
      for (const listener of this.#notificationListeners.get(method) ?? []) listener(params)
    }
  }
}

// Sentinel so connect() can tell an injected test factory (which may work in
// any environment) from the default browser-only WebSocket.
;(CortexRPC as unknown as { prototypeDefaultFactory?: unknown }).prototypeDefaultFactory = (url: string) => new WebSocket(url)

/** Singleton shared by the app shell; the daemon also reconnects through it. */
export const cortexRPC = new CortexRPC()

/**
 * Capability check (spec §4): the ONLY branching point between "native
 * superpowers" and "graceful degradation". Sets the `window.cortexConnected`
 * flag from the spec's contract and returns whether Path A is available.
 */
export async function checkCortexConnection(url?: string, timeoutMs?: number): Promise<boolean> {
  const connected = await cortexRPC.connect(url, timeoutMs)
  if (typeof window !== 'undefined') window.cortexConnected = connected
  return connected
}

/**
 * Deterministic idempotency key for one outbox batch: a flush retried after
 * a lost response produces the SAME key, so the daemon's queue dedupes it
 * instead of double-executing every entry. (A plain hash suffices — this is
 * dedup metadata, not a security boundary.)
 */
function batchIdFor(entries: SyncOutboxEntry[]): string {
  const data = JSON.stringify(entries.map((e) => [e.id, e.action, e.payload, e.queuedAt]))
  // cyrb53: a plain 53-bit string hash — dedup metadata, not security.
  let h1 = 0xdeadbeef
  let h2 = 0x41c6ce57
  for (let i = 0; i < data.length; i++) {
    const ch = data.charCodeAt(i)
    h1 = Math.imul(h1 ^ ch, 2654435761)
    h2 = Math.imul(h2 ^ ch, 1597334677)
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909)
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909)
  return `sync-${(h2 >>> 0).toString(16).padStart(8, '0')}${(h1 >>> 0).toString(16).padStart(8, '0')}`
}

/**
 * Path A transport (spec §4): hand the whole outbox to the daemon's
 * persistent queue. The daemon owns retry/execution timing (it can run while
 * the PWA tab is closed), so success here empties the local outbox at once.
 *
 * Each hand-off carries the batch's idempotency key (see `batchIdFor`).
 */
export class CortexSyncTransport implements SyncTransport {
  readonly #rpc: CortexRPC

  constructor(rpc: CortexRPC = cortexRPC) {
    this.#rpc = rpc
  }

  get available(): boolean {
    return this.#rpc.connected
  }

  async enqueueSyncTask(entries: SyncOutboxEntry[]): Promise<void> {
    const result = await this.#rpc.call<{ accepted?: number; deduplicated?: boolean } | null>('cortex.task.enqueue', {
      kind: 'syncNotes',
      batchId: await batchIdFor(entries),
      entries,
    })
    // The daemon must acknowledge the whole batch; anything else means we
    // cannot know the entries are durably queued, so the caller must degrade.
    if (result === null || typeof result !== 'object') throw new Error('cortex.task.enqueue returned no result')
    if (typeof result.accepted === 'number' && result.accepted !== entries.length) {
      throw new Error(`cortex.task.enqueue accepted ${result.accepted} of ${entries.length} entries`)
    }
  }
}
