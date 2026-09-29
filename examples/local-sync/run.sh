#!/bin/sh
# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
# One-command local-sync demo (examples/local-sync/README.md): engine build,
# mock endpoint, engine-backed daemon, one enqueued note, teardown.
set -eu
cd "$(dirname "$0")/../.."
echo "== building cortex-engine =="
(cd cortex-engine && cargo build >/dev/null)
ENGINE="../cortex-engine/target/debug/cortex-engine"
[ -f "../cortex-engine/target/debug/cortex-engine.exe" ] 2>/dev/null || true
if [ -x "cortex-engine/target/debug/cortex-engine.exe" ]; then
  ENGINE="../cortex-engine/target/debug/cortex-engine.exe"
fi

cleanup() {
  [ -n "${MOCK_PID:-}" ] && kill "$MOCK_PID" 2>/dev/null || true
  [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "== mock sync endpoint :9999 =="
(cd cortex-daemon && go run ./cmd/cortex-demo serve-mock -addr 127.0.0.1:9999) &
MOCK_PID=$!

echo "== daemon (engine-backed) =="
(cd cortex-daemon && go run ./cmd/cortex-daemon \
    -engine "$ENGINE" \
    -sync-endpoint http://127.0.0.1:9999/sync) &
DAEMON_PID=$!
sleep 2

echo "== enqueue one note =="
(cd cortex-daemon && go run ./cmd/cortex-demo sync-once -endpoint http://127.0.0.1:9999/sync)

echo "== done (the daemon output above shows the completed task) =="
