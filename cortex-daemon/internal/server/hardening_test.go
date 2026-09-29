// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/rpc"
)

func testServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	taskQueue := mustQueue(t)
	server := httptest.NewServer(NewHandler(context.Background(), cfg, taskQueue, rpc.NewBroker(), nil))
	t.Cleanup(server.Close)
	return server
}

func mustQueue(t *testing.T) *queue.Queue {
	t.Helper()
	taskQueue, err := queue.Open(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	return taskQueue
}

func dialRPC(t *testing.T, url string, opts *websocket.DialOptions) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return err
	}
	conn.Close(websocket.StatusNormalClosure, "")
	return nil
}

func TestAuthTokenEnforcedWhenConfigured(t *testing.T) {
	cfg := Config{AuthToken: "s3cret-token"}
	server := testServer(t, cfg)
	wsURL := "ws://" + strings.TrimPrefix(server.URL, "http://") + "/rpc"

	// No token, wrong header token, and wrong query token: all rejected.
	if err := dialRPC(t, wsURL, nil); err == nil {
		t.Fatal("upgrade without token must be rejected")
	}
	if err := dialRPC(t, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Cortex-Token": []string{"wrong"}},
	}); err == nil {
		t.Fatal("upgrade with wrong header token must be rejected")
	}
	if err := dialRPC(t, wsURL+"?token=nope", nil); err == nil {
		t.Fatal("upgrade with wrong query token must be rejected")
	}

	// Correct token via query param (the browser-friendly path)...
	if err := dialRPC(t, wsURL+"?token=s3cret-token", nil); err != nil {
		t.Fatalf("query-param token must be accepted: %v", err)
	}
	// ...and via header.
	if err := dialRPC(t, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Cortex-Token": []string{"s3cret-token"}},
	}); err != nil {
		t.Fatalf("header token must be accepted: %v", err)
	}
}

func TestAuthTokenDisabledByDefault(t *testing.T) {
	server := testServer(t, Config{})
	wsURL := "ws://" + strings.TrimPrefix(server.URL, "http://") + "/rpc"
	if err := dialRPC(t, wsURL, nil); err != nil {
		t.Fatalf("loopback upgrade without auth config must succeed: %v", err)
	}
}

func TestFSWriteRejectsOversizedPayloads(t *testing.T) {
	dir := t.TempDir()
	write := handlers(dir, mustQueue(t), nil)["fs.write"]
	if write == nil {
		t.Fatal("fs.write handler missing")
	}

	oversized := make([]byte, maxFSWriteBytes+1)
	params, _ := json.Marshal(map[string]string{
		"path":   "big.bin",
		"buffer": base64.StdEncoding.EncodeToString(oversized),
	})
	if _, err := write(context.Background(), params); err == nil {
		t.Fatal("oversized fs.write must be rejected")
	}

	// A small write inside the cap still lands in the sandbox.
	small, _ := json.Marshal(map[string]string{
		"path":   "ok.txt",
		"buffer": base64.StdEncoding.EncodeToString([]byte("hello")),
	})
	result, err := write(context.Background(), small)
	if err != nil {
		t.Fatalf("in-cap fs.write rejected: %v", err)
	}
	if result.(map[string]any)["bytesWritten"] != 5 {
		t.Fatalf("unexpected write result: %v", result)
	}
}

func TestConstantTimeEquals(t *testing.T) {
	if constantTimeEquals("abc", "abd") || constantTimeEquals("abc", "ab") || !constantTimeEquals("x", "x") {
		t.Fatal("constantTimeEquals misbehaves")
	}
}
