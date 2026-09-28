// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { describeOutcome, summarizeTasks } from './telemetry'
import type { FlushOutcome } from './sync'

describe('summarizeTasks', () => {
  it('counts daemon task states', () => {
    expect(
      summarizeTasks([
        { state: 'queued' },
        { state: 'queued' },
        { state: 'running' },
        { state: 'completed' },
        { state: 'completed' },
        { state: 'completed' },
        { state: 'failed' },
      ]),
    ).toEqual({ queued: 2, running: 1, completed: 3, failed: 1 })
  })

  it('returns all zeros for an empty queue and ignores unknown states', () => {
    expect(summarizeTasks([])).toEqual({ queued: 0, running: 0, completed: 0, failed: 0 })
    expect(summarizeTasks([{ state: 'mystery' }])).toEqual({ queued: 0, running: 0, completed: 0, failed: 0 })
  })
})

describe('describeOutcome', () => {
  const outcome = (over: Partial<FlushOutcome>): FlushOutcome => ({
    via: 'http',
    synced: 0,
    failed: 0,
    pending: 0,
    ...over,
  })

  it('describes the idle state', () => {
    expect(describeOutcome(null)).toBe('nothing to sync yet')
    expect(describeOutcome(outcome({ via: 'none' }))).toBe('nothing to sync yet')
  })

  it('describes daemon hand-offs and direct pushes, singular and plural', () => {
    expect(describeOutcome(outcome({ via: 'cortex', synced: 1 }))).toBe('1 entry handed to the daemon')
    expect(describeOutcome(outcome({ via: 'cortex', synced: 3 }))).toBe('3 entries handed to the daemon')
    expect(describeOutcome(outcome({ via: 'http', synced: 2 }))).toBe('2 entries pushed directly')
  })

  it('reports partial failures as waiting entries', () => {
    expect(describeOutcome(outcome({ via: 'http', synced: 1, failed: 2, pending: 2 }))).toBe(
      '1 entry pushed, 2 entries waiting for connectivity',
    )
  })
})
