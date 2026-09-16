# cortex-android

Android companion ([spec.txt](../spec.txt) §7): runs the Go `cortex-daemon` as a foreground service so the PWA gets true background execution, bridged over the same [JSON-RPC contract](../docs/jsonrpc-contract.md) as everywhere else.

## Build

```sh
./gradlew assembleDebug     # or assembleRelease
```

Requires JDK 17 and the Android SDK (API 35). The Gradle wrapper (8.9) is committed.

## Milestones

1. **This scaffold** — `DaemonService` (foreground, `dataSync` type) keeps the process alive; `CortexBridge` connects to `ws://127.0.0.1:9911/rpc` with reconnect; `MainActivity` hosts the PWA in a WebView and handles `tpt://` deep links.
2. **Embed the daemon** — bind `cortex-daemon` via [gomobile](https://pkg.go.dev/golang.org/x/mobile/bind) (it is cgo-free by design) and start it from `DaemonService`; the service lifecycle stays as-is.
3. **Direct APK distribution** — signed APKs published from this repo's releases, no Play Store.
