// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { SyncOutboxEntry } from './storage'
import type { SyncTransport } from './sync'

/** Status of the WebSocket link to the tpt-cortex daemon (spec §3 Layer 2). */
export type CortexStatus = 'disconnected' | 'connecting' | 'connected'

/** The daemon's fixed local endpoint (spec §3): loopback only, no TLS needed on localhost. */
export const CORTEX_WS_URL = 'ws://127.0.0.1:9911'

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

/**
 * JSON-RPC 2.0 client over the loopback WebSocket to cortex-daemon. The wire
 * contract is documented in docs/jsonrpc-contract.md and shared with the Go
 * daemon and the Android companion -- one contract, three runtimes.
 */
export class CortexRPC {
  #ws: WebSocket | null = null
  #pending = new Map<number, PendingCall>()
  #nextId = 1
  #statusListeners = new Set<(status: CortexStatus) => void>()
  #notificationListeners = new Map<string, Set<(params: unknown) => void>>()

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
   * the daemon is a normal state, not an error.
   */
  connect(url: string = CORTEX_WS_URL, timeoutMs = 2000): Promise<boolean> {
    if (this.connected) return Promise.resolve(true)
    if (typeof WebSocket === 'undefined') return Promise.resolve(false)
    this.#setStatus('connecting')
    return new Promise((resolve) => {
      let settled = false
      let ws: WebSocket
      try {
        ws = new WebSocket(url)
      } catch {
        this.#setStatus('disconnected')
        return resolve(false)
      }
      const finish = (connected: boolean) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        if (!connected) {
          ws.close()
          if (this.#ws === ws) this.#ws = null
          this.#setStatus('disconnected')
        }
        resolve(connected)
      }
      const timer = setTimeout(() => finish(false), timeoutMs)
      ws.addEventListener('open', () => {
        this.#ws = ws
        this.#setStatus('connected')
        finish(true)
      })
      ws.addEventListener('message', (event) => this.#dispatch(String(event.data)))
      ws.addEventListener('close', () => {
        if (this.#ws === ws) {
          this.#ws = null
          this.#setStatus('disconnected')
        }
        finish(false)
      })
      ws.addEventListener('error', () => finish(false))
    })
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
 * Path A transport (spec §4): hand the whole outbox to the daemon's
 * persistent queue. The daemon owns retry/execution timing (it can run while
 * the PWA tab is closed), so success here empties the local outbox at once.
 */
export class CortexSyncTransport implements SyncTransport {
  get available(): boolean {
    return cortexRPC.connected
  }

  async enqueueSyncTask(entries: SyncOutboxEntry[]): Promise<void> {
    await cortexRPC.call('cortex.task.enqueue', { kind: 'syncNotes', entries })
  }
}
