# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| latest release | yes |
| older releases | best effort — upgrade first |

## Reporting a vulnerability

Email **security@tpt.solutions** (or open a GitHub security advisory on this
repository). Please include a description, reproduction steps or a hostile
`.ctx` script / RPC payload, and the affected components. You will get an
acknowledgement within 5 business days. We credit reporters in the release
notes unless asked otherwise. Please do not open public issues for
vulnerabilities.

## Trust boundaries and invariants

tpt-pwa is a local-first system with one deliberate trust boundary: **the
daemon is the only component with OS access**; the PWA and the `.ctx` scripts
it runs are untrusted by design.

### 1. Loopback binding (cortex-daemon)

The daemon binds `127.0.0.1:9911` by default and **must never** listen on a
non-loopback address — `cortex-daemon doctor` fails the `origin` check if the
configured address is not loopback. Anything on the machine can reach the
port, which is why:

- a shared `-auth-token` is supported and constant-time compared (401
  otherwise); **the Android companion always requires one** (`mobile.Start`
  rejects an empty token) because any installed app can reach the loopback
  port;
- WebSocket upgrades are answered only for `localhost:*` / `127.0.0.1:*`
  origins.

### 2. The `fs.write` sandbox

`fs.write` resolves **symlinks** (and Windows junctions) on both the sandbox
root and the destination parent before its containment check, so a link
planted inside the data dir cannot point outside. Decoded payloads above
4 MiB are rejected, and writes land via temp file + rename (no truncated
files).

### 3. The cortex-engine VM (untrusted scripts)

`.ctx` scripts are hostile input. The engine's totality contract
(cortex-engine/src/lib.rs, docs/formal-verification.md):

- no `unsafe`, no panics on script input, no I/O except through the
  host-provided `NativeEnv`;
- parser nesting depth (128) and token (100k) caps bound parser/compiler/Drop
  recursion — deep nesting is a rejected parse, not a stack overflow;
- a hard instruction budget bounds every loop, and a 128-frame call-depth
  cap bounds recursion memory;
- data errors (index range, integer overflow, division by zero, NaN
  ordering) are distinct error values; the CLI exits 2 for permanent
  (parse/compile) failures so the daemon stops retrying them.

**Capability manifests**: before a task script executes a single native
call, the daemon asks the engine for its static native surface
(`cortex-engine manifest`) and checks it against `-allow-natives` (unset =
all allowed). A script exceeding the allowlist parks as permanently failed
without any effect firing — and because the manifest is a full compile,
only scripts that would actually run can declare capabilities at all.

**`native.db.*` is real SQL**: task scripts read and write the daemon's
SQLite database (`<data-dir>/cortex.db`) — their durable scratch space.
The database contains only what task scripts put there (the task queue and
the PWA's notes live elsewhere by design), and the same `-allow-natives`
gate decides whether a script may touch it at all.

**Watch paths are sandboxed twice**: a task's `watch` glob is validated at
enqueue (relative, no `..` traversal, no absolute paths) and re-contained
against the physical data dir — symlinks resolved — on every watcher
resync. The watcher is non-recursive and only signals that a matching file
changed; it exposes no file contents to the script beyond what the script's
own natives ( gated by `-allow-natives`) can read.

### 4. Sync payloads

Outbox entries are handed to the configured sync endpoint as JSON with the
entry id (for server-side dedup) and a content-derived batch id (so retried
hand-offs deduplicate). Permanent 4xx responses dead-letter entries instead
of retrying forever. There is **no telemetry and no third-party network
call** anywhere in the stack.

### 5. Token handling

Tokens are supplied via flag/config (never written to disk by the daemon),
sent as `?token=` (browser WebSocket APIs cannot set headers) or
`X-Cortex-Token`, and are constant-time compared. Prefer a fresh random
token per device pairing.

## Known limitations

- The Android debug APK is unsigned; only release-tagged artifacts are
  signed (when the `APK_KEYSTORE_*` secrets are configured).
- `fs.write` enforces containment, not quotas: a token holder can still fill
  the disk within the 4 MiB-per-write limit.
- The engine's `db.*` natives see exactly what the daemon's host process
  exposes — today that is the task's own outbox rows (see
  docs/jsonrpc-contract.md).
