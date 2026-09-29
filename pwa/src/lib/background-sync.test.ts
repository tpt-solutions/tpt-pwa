// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { requestBackgroundSync } from './background-sync'

describe('requestBackgroundSync', () => {
  it('is a safe no-op where the Service Worker API is absent (bare Node)', async () => {
    await expect(requestBackgroundSync()).resolves.toBeUndefined()
  })
})
