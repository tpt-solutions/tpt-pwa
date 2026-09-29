// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package mobile exposes the daemon for gomobile bind so cortex-android can
// embed it inside DaemonService (spec §7). The API surface is deliberately
// tiny — gomobile only binds simple types:
//
//	gomobile bind -o app/libs/cortex.aar -target=android \
//	    -javapkg=solutions.tpt.cortex ./mobile
//
// which produces solutions.tpt.cortex.Mobile with start/stop.
package mobile

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/server"
)

var (
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
)

// Start launches the daemon (loopback WebSocket server + queue scheduler) on
// background goroutines and returns immediately. Idempotent while running.
// queueDir is created on demand and holds queue.json plus the fs.write
// sandbox.
//
// token is REQUIRED: on a phone every app can reach the loopback port, so
// /rpc upgrades must present it (the host app hands the same token to the
// WebView's PWA, e.g. as the `token` query parameter). An empty token fails
// rather than silently trusting the whole device.
func Start(addr, queueDir, syncEndpoint, token string) error {
	mu.Lock()
	if running {
		mu.Unlock()
		return nil
	}
	if addr == "" {
		addr = "127.0.0.1:9911"
	}
	if syncEndpoint == "" {
		syncEndpoint = "https://api.tpt/sync"
	}
	if queueDir == "" {
		mu.Unlock()
		return errors.New("mobile: queueDir is required")
	}
	if token == "" {
		mu.Unlock()
		return errors.New("mobile: token is required (shared secret for /rpc)")
	}
	ctx, ctxCancel := context.WithCancel(context.Background())
	cfg := server.Config{
		Addr:         addr,
		SyncEndpoint: syncEndpoint,
		QueuePath:    filepath.Join(queueDir, "queue.json"),
		DataDir:      filepath.Join(queueDir, "data"),
		PollEvery:    5 * time.Second,
		MaxAttempts:  8,
		AuthToken:    token,
	}
	cancel = ctxCancel
	running = true
	mu.Unlock()

	go func() {
		// Run returns when the listener stops (Stop or bind teardown).
		if err := server.Run(ctx, cfg); err != nil && ctx.Err() == nil {
			// Listener failed (e.g. port taken): drop the running flag so a
			// later Start can retry.
			mu.Lock()
			running = false
			mu.Unlock()
		}
	}()
	return nil
}

// Stop shuts the daemon down; a no-op when not running.
func Stop() error {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	running = false
	return nil
}
