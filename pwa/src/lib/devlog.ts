// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

/**
 * Development-only error surfacing. The app's degradation paths swallow
 * errors *by design* in production (a failed optional enhancement must never
 * break the UI), but silent catch blocks make development miserable. This
 * warns behind the DEV flag only -- production behavior is unchanged.
 */
export function warnDev(scope: string, error: unknown): void {
  if (import.meta.env?.DEV) {
    console.warn(`[tpt-pwa:${scope}]`, error)
  }
}
