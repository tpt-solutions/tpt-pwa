// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// cortex-daemon is the tpt-cortex native companion's IPC host (spec §3
// Layer 3): a WebSocket JSON-RPC server on the loopback, a persistent task
// queue, and a connectivity-aware scheduler. The PWA talks to it per
// docs/jsonrpc-contract.md. Runtime assembly lives in internal/server; this
// binary only maps flags onto it.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9911", "listen address (loopback only by design)")
	syncEndpoint := flag.String("sync-endpoint", "https://api.tpt/sync", "remote sync endpoint")
	poll := flag.Duration("poll", 5*time.Second, "scheduler poll interval")
	maxAttempts := flag.Int("max-attempts", 8, "max task attempts before parking as failed")
	queuePath := flag.String("queue", defaultQueuePath(), "persistent task queue file")
	dataDir := flag.String("data-dir", defaultDataDir(), "sandbox root for fs.write")
	enginePath := flag.String("engine", "", "cortex-engine binary; when set, tasks execute through the DSL VM (spec §6)")
	engineScript := flag.String("engine-script", "", "DSL script for -engine (default: embedded sync.ctx)")
	authToken := flag.String("auth-token", "", "require this shared token on /rpc upgrades (query `token` or X-Cortex-Token header); empty = loopback trust")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.LUTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Run(ctx, server.Config{
		Addr:         *addr,
		SyncEndpoint: *syncEndpoint,
		QueuePath:    *queuePath,
		DataDir:      *dataDir,
		PollEvery:    *poll,
		MaxAttempts:  *maxAttempts,
		EnginePath:   *enginePath,
		EngineScript: *engineScript,
		AuthToken:    *authToken,
	}); err != nil {
		log.Fatal(err)
	}
}

func defaultQueuePath() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "tpt-cortex", "queue.json")
	}
	return "queue.json"
}

func defaultDataDir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "tpt-cortex", "data")
	}
	return "data"
}
