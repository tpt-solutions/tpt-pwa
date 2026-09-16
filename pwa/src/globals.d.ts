// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

declare global {
  interface Window {
    /**
     * Capability flag from spec §6: true when the tpt-cortex daemon answered
     * on the local WebSocket. Application code branches on this (via
     * `checkCortexConnection()`), never on OS/user-agent sniffing.
     */
    cortexConnected: boolean
  }
}

export {}
