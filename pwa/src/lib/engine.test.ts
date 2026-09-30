// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { describeRunOutcome, formatValue, type RunReport } from './engine'

describe('describeRunOutcome', () => {
  it('formats successful results through the value formatter', () => {
    const report: RunReport = { ok: true, result: [1, 'two', null] }
    expect(describeRunOutcome(report)).toBe('[1, two, null]')
  })

  it('reports permanent failures as script bugs', () => {
    const report: RunReport = { ok: false, class: 'permanent', error: 'parse error: unexpected token' }
    expect(describeRunOutcome(report)).toBe('parse error: unexpected token (permanent — fix the script)')
  })

  it('reports transient failures as data problems', () => {
    const report: RunReport = { ok: false, class: 'transient', error: 'vm error: division by zero' }
    expect(describeRunOutcome(report)).toBe('vm error: division by zero (transient — data-dependent)')
  })
})

describe('formatValue', () => {
  it('renders the engine value shapes readably', () => {
    expect(formatValue(null)).toBe('null')
    expect(formatValue(42)).toBe('42')
    expect(formatValue(1.5)).toBe('1.5')
    expect(formatValue(true)).toBe('true')
    expect(formatValue('hi')).toBe('hi')
    expect(formatValue([1, 2])).toBe('[1, 2]')
    expect(formatValue({ id: '41' })).toBe('{"id":"41"}')
    expect(formatValue([{ nested: true }])).toBe('[{"nested":true}]')
  })
})
