# Troubleshooting & FAQ

## The PWA shows "fallback mode" (no daemon)

1. Is the daemon running? `pnpm run dev:daemon` (from the repo root) or your
   installed binary. It logs `listening on ws://127.0.0.1:9911/rpc`.
2. Run the pre-flight check: `cortex-daemon doctor`. The `port` check tells
   you whether something already owns 9911.
3. Browser console: `window.cortexConnected` should flip to `true` within
   ~2s of the daemon starting; the client retries with backoff once a link
   has been established.

### Port 9911 already in use

Another daemon instance (fine — the PWA will use it) or an unrelated
process. Find it: `netstat -ano | findstr 9911` (Windows) /
`lsof -i :9911` (macOS/Linux). Move the daemon with `-addr 127.0.0.1:9912`
— there is no config file on purpose; whatever launches the daemon owns the
flags.

### Windows firewall prompt

The daemon binds the **loopback** interface only. Windows Defender may still
show a firewall prompt on first run; allowing it is harmless, and you can
deny any prompt that asks for *public network* access — loopback traffic
never leaves the machine. If you accidentally blocked loopback, the PWA's
connect probe times out after 2s every attempt: remove the block rule for
the binary.

### The queue file is corrupt (daemon refuses to start — old versions)

Since the hardening pass the daemon **never** refuses to boot over a corrupt
queue: the file is moved aside to `queue.json.corrupt-<timestamp>` and the
daemon starts fresh. If you're on an older build, do that move by hand.

## Android companion

### The WebView PWA can't reach the daemon (cleartext loopback)

Android treats `ws://` (cleartext) as blocked by default. The companion's
network security config permits cleartext **to loopback only**
(`cortex-android/app/src/main/AndroidManifest.xml` +
`network_security_config.xml`). If you ship a fork, keep that scoping — do
not open cleartext globally.

### "embedded daemon not packaged"

The APK was built without the gomobile `.aar`. That's expected for `gradlew
assembleDebug` without the daemon source built; the release workflow builds
it (`gomobile bind`). To do it locally:

```sh
go install golang.org/x/mobile/cmd/gomobile@latest
cd cortex-daemon && gomobile bind -o ../cortex-android/app/libs/cortex.aar \
  -target=android -androidapi 26 -javapkg=solutions.tpt.cortex ./mobile
```

### Token mismatch (`401` on /rpc)

The service generates a per-boot token, hands it to `Mobile.start` and to
the WebView via the `cortex:auth` DOM event. A 401 in the WebView means the
PWA connected before the token arrived — it retries on `cortex:auth`. If you
forked the bridge, make sure both sides use the SAME token source.

## Engine / `.ctx` scripts

- **Exit code 2** (parse/compile): the script bytes are bad; the scheduler
  parks the task without retrying. Run
  `cortex-engine run script.ctx` to see the error with line:col.
- **Exit code 1** (runtime): data-shaped failure (index out of range,
  overflow, native error). The scheduler retries with backoff, then parks it
  as `failed`.
- **BudgetExhausted**: the task exceeded 1M instructions — check for huge
  lists in `for` loops.
- Deep nesting rejected at parse time (limit 128) is a security bound, not a
  style opinion — see [formal-verification.md](formal-verification.md).

## Sync

- **Entries stay queued forever**: with no daemon AND no HTTP endpoint
  configured, the PWA intentionally holds entries (there is no placeholder
  endpoint to POST into the void). Bring up the daemon, or configure a real
  endpoint.
- **HTTP 4xx entries vanish from the queue**: they're dead-lettered, not
  lost — inspect via storage (`dead_letters`).
- **Duplicate pushes after a crash**: shouldn't happen — hand-offs carry a
  content-derived `batchId` the daemon dedupes, and pushes carry the entry
  id for server-side dedup. If you see duplicates, check that your endpoint
  honors the `id` field ([jsonrpc-contract.md](jsonrpc-contract.md)).

## Building / developing

- **`prepare-sidecar` fails with "no cortex-daemon binary found"**: build it
  — `cd cortex-daemon && go build -o bin/cortex-daemon-<triple>
  ./cmd/cortex-daemon` — or opt into a 0-byte placeholder with
  `CORTEX_SIDECAR_ALLOW_PLACEHOLDER=1` (dev only: bundling a placeholder
  ships a shell without a working daemon).
- **`pnpm run dev:daemon` fails with "cannot find main module"**: you're on
  an old checkout — the script was fixed to `cd cortex-daemon` first.
- **Tauri `cargo check` needs system libs on Linux**: libwebkit2gtk-4.1,
  libappindicator3, librsvg, patchelf (see the CI shell job for the exact
  list).
