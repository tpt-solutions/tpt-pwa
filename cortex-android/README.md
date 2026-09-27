# cortex-android

Android companion ([spec.txt](../spec.txt) §7): runs the Go `cortex-daemon` as a foreground service so the PWA gets true background execution, bridged over the same [JSON-RPC contract](../docs/jsonrpc-contract.md) as everywhere else.

## Build

```sh
./gradlew assembleDebug testDebugUnitTest   # or assembleRelease
```

Requires JDK 17 and the Android SDK (API 35). The Gradle wrapper (8.9) is committed.

## How the pieces fit

- **`DaemonService`** (foreground, `dataSync` type) keeps the process alive, starts the embedded Go daemon when packaged, and owns the `CortexBridge`.
- **`CortexBridge`** is a full JSON-RPC 2.0 client (id-matched requests, notification dispatch, `enqueueSync`) — unit-tested against the contract without sockets.
- **`MainActivity`** hosts the PWA in a WebView and handles `tpt://` deep links. The PWA inside connects **directly** to `ws://127.0.0.1:9911/rpc` — `res/xml/network_security_config.xml` permits cleartext for the loopback only. Daemon notifications are additionally relayed into the WebView as `cortex:notification` DOM events, covering reconnect windows (the PWA listens in `app.ts`).

## Embedding the Go daemon (gomobile)

The daemon is cgo-free by design, so it binds cleanly:

```sh
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
cd cortex-daemon
gomobile bind -o ../cortex-android/app/libs/cortex.aar -target=android \
    -javapkg=solutions.tpt.cortex ./mobile
```

That produces `solutions.tpt.cortex.Mobile` (`start(addr, queueDir, syncEndpoint)` / `stop()`). `DaemonService` loads it reflectively: with the .aar packaged the daemon runs in-process; without it, the service still keeps the bridge (and liveness) going and the PWA stays in graceful-degradation mode.

## Direct APK distribution (no Play Store)

Tag a release (`git tag v0.1.0 && git push --tags`) and [.github/workflows/release.yml](../.github/workflows/release.yml) builds `assembleRelease` + `assembleDebug` and attaches both APKs to the GitHub release. The PWA links there from `pwa/public/companion.html` — the user downloads, opens, and Android's installer takes over. Release signing: add a keystore to the workflow secrets and wire `signingConfigs` in `app/build.gradle.kts` (currently unsigned release artifact).
