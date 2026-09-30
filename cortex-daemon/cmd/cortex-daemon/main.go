// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// cortex-daemon is the tpt-cortex native companion's IPC host (spec §3
// Layer 3): a WebSocket JSON-RPC server on the loopback, a persistent task
// queue, and a connectivity-aware scheduler. The PWA talks to it per
// docs/jsonrpc-contract.md. Runtime assembly lives in internal/server; this
// binary only maps flags (with optional -config file values underneath)
// onto it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/config"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/doctor"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		doctorCommand(os.Args[2:])
		return
	}
	runDaemon(os.Args[1:])
}

func runDaemon(args []string) {
	// Precedence: explicit flag > config file > built-in default. The file
	// must be known before the other flags exist (it seeds their defaults),
	// so it is extracted in a pre-scan and the real flag only documents it.
	file, err := config.Load(extractConfigPath(args))
	if err != nil {
		log.Fatal(err)
	}

	// Registered for -h; the value was already consumed by extractConfigPath.
	flag.String("config", "", "JSON config file; keys mirror the flags, every flag overrides it")
	addr := flag.String("addr", orString(file.Addr, "127.0.0.1:9911"), "listen address (loopback only by design)")
	syncEndpoint := flag.String("sync-endpoint", orString(file.SyncEndpoint, "https://api.tpt/sync"), "remote sync endpoint")
	poll := flag.Duration("poll", orDuration(file.Poll, 5*time.Second), "scheduler poll interval")
	maxAttempts := flag.Int("max-attempts", orInt(file.MaxAttempts, 8), "max task attempts before parking as failed")
	queuePath := flag.String("queue", orString(file.QueuePath, defaultQueuePath()), "persistent task queue file")
	dataDir := flag.String("data-dir", orString(file.DataDir, defaultDataDir()), "sandbox root for fs.write")
	enginePath := flag.String("engine", orString(file.EnginePath, ""), "cortex-engine binary; when set, tasks execute through the DSL VM (spec §6)")
	engineScript := flag.String("engine-script", orString(file.EngineScript, ""), "DSL script for -engine (default: embedded sync.ctx)")
	authToken := flag.String("auth-token", orString(file.AuthToken, ""), "require this shared token on /rpc upgrades (query `token` or X-Cortex-Token header); empty = loopback trust")
	logFormat := flag.String("log-format", orString(file.LogFormat, "text"), "log output format: text or json")
	logLevel := flag.String("log-level", orString(file.LogLevel, "info"), "log level: debug, info, warn, or error")
	flag.CommandLine.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
		LogFormat:    *logFormat,
		LogLevel:     *logLevel,
	}); err != nil {
		log.Fatal(err)
	}
}

// extractConfigPath finds -config/--config in args before flag parsing (the
// other flags need the file's values as their defaults). `--` ends the scan.
func extractConfigPath(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "-config" || arg == "--config" {
			if i+1 >= len(args) {
				log.Fatal("-config requires a file path")
			}
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "-config="); ok {
			return value
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok {
			return value
		}
	}
	return ""
}

// doctorCommand implements `cortex-daemon doctor`: pre-flight checks over the
// same flags (and -config file) the daemon takes, linked from the PWA's
// cortex status chip.
func doctorCommand(args []string) {
	file, err := config.Load(extractConfigPath(args))
	if err != nil {
		log.Fatal(err)
	}
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	// Registered for -h; the value was already consumed by extractConfigPath.
	fs.String("config", "", "JSON config file; keys mirror the flags, every flag overrides it")
	addr := fs.String("addr", orString(file.Addr, "127.0.0.1:9911"), "address the daemon will bind")
	syncEndpoint := fs.String("sync-endpoint", orString(file.SyncEndpoint, "https://api.tpt/sync"), "remote sync endpoint")
	queuePath := fs.String("queue", orString(file.QueuePath, defaultQueuePath()), "persistent task queue file")
	dataDir := fs.String("data-dir", orString(file.DataDir, defaultDataDir()), "sandbox root for fs.write")
	enginePath := fs.String("engine", orString(file.EnginePath, ""), "cortex-engine binary (optional)")
	authToken := fs.String("auth-token", orString(file.AuthToken, ""), "auth token that will be required on /rpc (optional)")
	asJSON := fs.Bool("json", false, "emit checks as JSON")
	fs.Parse(args)

	opts := doctor.Options{
		Addr:         *addr,
		SyncEndpoint: *syncEndpoint,
		QueuePath:    *queuePath,
		DataDir:      *dataDir,
		EnginePath:   *enginePath,
		AuthToken:    *authToken,
	}
	if *asJSON {
		encoded, err := doctor.JSON(doctor.All(context.Background(), opts))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(encoded))
		return
	}
	healthy := doctor.Run(context.Background(), opts, os.Stdout)
	if !healthy {
		os.Exit(1)
	}
}

// orString/orDuration/orInt implement "file value if set, else the default":
// zero values in the config File mean the key was absent.
func orString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func orDuration(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		// config.Load validated this already; unreachable in practice.
		slog.Warn("invalid poll duration; using default", "value", value)
		return fallback
	}
	return parsed
}

func orInt(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
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
