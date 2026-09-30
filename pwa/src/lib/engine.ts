// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

/**
 * Typed wrapper around the wasm-built cortex-engine (src/lib/engine-wasm/,
 * built from cortex-engine-wasm/ by `pnpm run engine:build`). The playground
 * runs the SAME lexer → parser → compiler → VM pipeline as the daemon,
 * against a deterministic in-memory sandbox — no network, no filesystem;
 * `http.post` and `db.exec` effects are recorded and shown instead.
 *
 * The module (and the 260KB wasm) loads lazily: opening the playground is
 * the only thing that pays for it.
 */

export type RunReport =
  | { ok: true; result: unknown }
  | { ok: false; class: 'permanent' | 'transient'; error: string }

/** Error classes mirror the CLI's exit codes: 2 = permanent, 1 = transient. */
export function describeRunOutcome(report: RunReport): string {
  if (report.ok) return formatValue(report.result)
  const hint = report.class === 'permanent' ? 'permanent — fix the script' : 'transient — data-dependent'
  return `${report.error} (${hint})`
}

export function formatValue(value: unknown): string {
  if (value === null) return 'null'
  if (Array.isArray(value)) return `[${value.map(formatValue).join(', ')}]`
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

export type EngineModule = {
  Playground: new () => EnginePlayground
}

export type EnginePlayground = {
  set_rows(json: string): void
  set_connected(connected: boolean): void
  run(source: string): string
  manifest(source: string): string
  posts(): string
  executed_sql(): string
}

let modulePromise: Promise<EngineModule> | null = null

/** Load (once) and instantiate the wasm engine. */
export async function loadEngine(): Promise<EngineModule> {
  modulePromise ??= (async () => {
    // Generated glue (wasm-pack, target web); excluded from type-checking,
    // hence the double cast.
    const factory = (await import('./engine-wasm/cortex-engine')) as unknown as EngineModule & {
      default: () => Promise<unknown>
    }
    await factory.default()
    return factory
  })()
  return modulePromise
}
