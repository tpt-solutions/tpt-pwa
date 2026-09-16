// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Ambient declarations for wa-sqlite subpath imports that ship JS without
// types. The main 'wa-sqlite' entry is typed via the package's own
// src/types/index.d.ts.

declare module 'wa-sqlite/dist/wa-sqlite-async.mjs' {
  /** ESM factory for the async wa-sqlite Wasm build; resolves to the module exports consumed by SQLite.Factory(). */
  export default function SQLiteAsyncESMFactory(): Promise<object>
}

declare module 'wa-sqlite/src/examples/OriginPrivateFileSystemVFS.js' {
  /** VFS that stores database pages as sync-access handles inside the Origin Private File System. */
  export class OriginPrivateFileSystemVFS {
    constructor()
    readonly name: string
    close(): Promise<void>
  }
}
