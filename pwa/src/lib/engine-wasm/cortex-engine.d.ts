/* tslint:disable */
/* eslint-disable */

/**
 * One sandbox instance: configurable fake rows + connectivity flag, and
 * every effect a run produces is recorded for the UI.
 */
export class Playground {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * SQL effects recorded by `run`: `[[sql, [params…]], …]` in call order.
     */
    executed_sql(): string;
    /**
     * The script's static native surface (the capability manifest, same
     * form the daemon's `-allow-natives` checks): `["db.query", …]`.
     * `Err` carries the parse/compile failure.
     */
    manifest(source: string): string;
    constructor();
    /**
     * Effects recorded by `run`: `[[url, body], …]` in call order.
     */
    posts(): string;
    /**
     * Parse, compile and run a `.ctx` script against the sandbox. The
     * result is a JSON object:
     * `{"ok":true,"result":<value>}` or
     * `{"ok":false,"class":"permanent"|"transient","error":"…"}` — the same
     * permanent/transient classes the CLI reports via exit codes 2/1.
     */
    run(source: string): string;
    /**
     * What `native.net.isConnected()` answers.
     */
    set_connected(connected: boolean): void;
    /**
     * Configure the rows `native.db.query` answers with: a JSON array of
     * objects (`[{"id": "41"}, …]`). Returns an error message for JSON
     * that is not an array.
     */
    set_rows(json: string): void;
}

export type InitInput = RequestInfo | URL | Response | BufferSource | WebAssembly.Module;

export interface InitOutput {
    readonly memory: WebAssembly.Memory;
    readonly __wbg_playground_free: (a: number, b: number) => void;
    readonly playground_executed_sql: (a: number) => [number, number];
    readonly playground_manifest: (a: number, b: number, c: number) => [number, number, number, number];
    readonly playground_new: () => number;
    readonly playground_posts: (a: number) => [number, number];
    readonly playground_run: (a: number, b: number, c: number) => [number, number];
    readonly playground_set_connected: (a: number, b: number) => void;
    readonly playground_set_rows: (a: number, b: number, c: number) => [number, number];
    readonly __wbindgen_externrefs: WebAssembly.Table;
    readonly __wbindgen_free: (a: number, b: number, c: number) => void;
    readonly __wbindgen_malloc: (a: number, b: number) => number;
    readonly __wbindgen_realloc: (a: number, b: number, c: number, d: number) => number;
    readonly __externref_table_dealloc: (a: number) => void;
    readonly __wbindgen_start: () => void;
}

export type SyncInitInput = BufferSource | WebAssembly.Module;

/**
 * Instantiates the given `module`, which can either be bytes or
 * a precompiled `WebAssembly.Module`.
 *
 * @param {{ module: SyncInitInput }} module - Passing `SyncInitInput` directly is deprecated.
 *
 * @returns {InitOutput}
 */
export function initSync(module: { module: SyncInitInput } | SyncInitInput): InitOutput;

/**
 * If `module_or_path` is {RequestInfo} or {URL}, makes a request and
 * for everything else, calls `WebAssembly.instantiate` directly.
 *
 * @param {{ module_or_path: InitInput | Promise<InitInput> }} module_or_path - Passing `InitInput` directly is deprecated.
 *
 * @returns {Promise<InitOutput>}
 */
export default function __wbg_init (module_or_path?: { module_or_path: InitInput | Promise<InitInput> } | InitInput | Promise<InitInput>): Promise<InitOutput>;
