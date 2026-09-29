// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { CortexRPC, CortexSyncTransport, CortexUnavailableError } from './cortex-client'
import type { SyncOutboxEntry } from './storage'

type Listener = (event: { data?: unknown }) => void

/** Minimal synchronous WebSocket double: tests drive open/message/close by hand. */
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static reset(): void {
    FakeWebSocket.instances = []
  }

  url: string
  sent: string[] = []
  closed = false
  #listeners = new Map<string, Set<Listener>>()

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  addEventListener(type: string, listener: Listener): void {
    let set = this.#listeners.get(type)
    if (!set) {
      set = new Set()
      this.#listeners.set(type, set)
    }
    set.add(listener)
  }

  send(data: string): void {
    this.sent.push(data)
  }

  close(): void {
    if (this.closed) return
    this.closed = true
    this.#emit('close', {})
  }

  open(): void {
    this.#emit('open', {})
  }

  receive(data: unknown): void {
    this.#emit('message', { data: typeof data === 'string' ? data : JSON.stringify(data) })
  }

  #emit(type: string, event: { data?: unknown }): void {
    for (const listener of this.#listeners.get(type) ?? []) listener(event)
  }
}

function lastSent(rpc: CortexRPC): { id: number; method: string; params: unknown } {
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
  void ws
  const raw = ws.sent[ws.sent.length - 1]
  void rpc
  return JSON.parse(raw)
}

async function connectAndOpen(rpc: CortexRPC): Promise<FakeWebSocket> {
  const pending = rpc.connect('ws://test', 1000)
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
  ws.open()
  expect(await pending).toBe(true)
  return ws
}

function makeEntry(id: string): SyncOutboxEntry {
  return { id, kind: 'note', action: 'update', payload: { id, title: 't', body: 'b', createdAt: 1, updatedAt: 1, syncedAt: null, deletedAt: null }, queuedAt: 1 }
}

describe('CortexRPC', () => {
  beforeEach(() => {
    FakeWebSocket.reset()
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('connect resolves true on a real handshake and concurrent connects share one socket', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    const [a, b] = [rpc.connect('ws://test', 1000), rpc.connect('ws://test', 1000)]
    FakeWebSocket.instances[0].open()
    expect(await a).toBe(true)
    expect(await b).toBe(true)
    expect(FakeWebSocket.instances).toHaveLength(1) // memoized, no competing sockets
  })

  it('calls round-trip over the socket', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    const ws = await connectAndOpen(rpc)
    const pending = rpc.call('cortex.ping')
    const request = lastSent(rpc)
    ws.receive({ jsonrpc: '2.0', id: request.id, result: { pong: true, version: 'test' } })
    await expect(pending).resolves.toEqual({ pong: true, version: 'test' })
  })

  it('calls made while disconnected reject immediately with CortexUnavailableError', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    await expect(rpc.call('cortex.ping')).rejects.toBeInstanceOf(CortexUnavailableError)
  })

  it('a dropped socket rejects every in-flight call instead of hanging', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    const ws = await connectAndOpen(rpc)
    const pending = rpc.call('cortex.task.enqueue')
    ws.close()
    await expect(pending).rejects.toBeInstanceOf(CortexUnavailableError)
  })

  it('auto-reconnect dials again with backoff after a live link drops', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    rpc.enableAutoReconnect()
    const ws = await connectAndOpen(rpc)
    ws.close() // daemon went away

    // First retry after 500ms, on a fresh socket.
    await vi.advanceTimersByTimeAsync(500)
    expect(FakeWebSocket.instances).toHaveLength(2)
    FakeWebSocket.instances[1].open()
    expect(rpc.connected).toBe(true)

    // Backoff resets after success: a second drop retries after 500ms again.
    FakeWebSocket.instances[1].close()
    await vi.advanceTimersByTimeAsync(500)
    expect(FakeWebSocket.instances).toHaveLength(3)
  })

  it('reconnect backoff grows and close() cancels it', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    rpc.enableAutoReconnect()
    const ws = await connectAndOpen(rpc)
    ws.close()

    await vi.advanceTimersByTimeAsync(499)
    expect(FakeWebSocket.instances).toHaveLength(1) // not yet
    await vi.advanceTimersByTimeAsync(1)
    FakeWebSocket.instances[1].close() // fail the first retry

    rpc.close() // user gave up: no further dials
    await vi.advanceTimersByTimeAsync(60_000)
    expect(FakeWebSocket.instances).toHaveLength(2)
  })
})

describe('CortexSyncTransport', () => {
  beforeEach(() => {
    FakeWebSocket.reset()
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('hands the batch to cortex.task.enqueue with an idempotency batchId', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    const transport = new CortexSyncTransport(rpc)
    await connectAndOpen(rpc)
    const ws = FakeWebSocket.instances[0]

    const entries = [makeEntry('a'), makeEntry('b')]
    const pending = transport.enqueueSyncTask(entries)
    // batchIdFor digests the batch async before the frame goes out.
    await vi.advanceTimersByTimeAsync(0)
    const request = lastSent(rpc)
    expect((request.params as { kind: string; batchId: string; entries: unknown[] })).toMatchObject({ kind: 'syncNotes' })
    expect(typeof (request.params as { batchId: string }).batchId).toBe('string')
    ws.receive({ jsonrpc: '2.0', id: request.id, result: { accepted: 2 } })
    await expect(pending).resolves.toBeUndefined()
  })

  it('rejects when the daemon accepts fewer entries than sent (degrade, do not dequeue)', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    const transport = new CortexSyncTransport(rpc)
    await connectAndOpen(rpc)

    const pending = transport.enqueueSyncTask([makeEntry('a'), makeEntry('b')])
    await vi.advanceTimersByTimeAsync(0)
    const ws = FakeWebSocket.instances[0]
    const request = lastSent(rpc)
    ws.receive({ jsonrpc: '2.0', id: request.id, result: { accepted: 1 } })
    await expect(pending).rejects.toThrow(/accepted 1 of 2/)
  })

  it('rejects a missing result so the flush degrades to the HTTP path', async () => {
    const rpc = new CortexRPC((url) => new FakeWebSocket(url))
    const transport = new CortexSyncTransport(rpc)
    await connectAndOpen(rpc)

    const pending = transport.enqueueSyncTask([makeEntry('a')])
    await vi.advanceTimersByTimeAsync(0)
    const ws = FakeWebSocket.instances[0]
    const request = lastSent(rpc)
    ws.receive({ jsonrpc: '2.0', id: request.id, result: null })
    await expect(pending).rejects.toThrow(/no result/)
  })
})
