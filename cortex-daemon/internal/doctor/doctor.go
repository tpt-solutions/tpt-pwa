// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package doctor runs the environment checks a user needs before blaming the
// daemon: port availability, auth token configuration, WebSocket origin
// expectations, engine binary/version, and queue health. `cortex-daemon
// doctor` prints each check with an OK/FAIL/WARN verdict and exits non-zero
// when something is actually broken (warnings do not fail).
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/server"
)

// Check is one diagnostic with a verdict.
type Check struct {
	Name    string
	Verdict string // "ok", "warn", "fail"
	Detail  string
}

// Options selects what doctor inspects (mirrors the daemon's flags).
type Options struct {
	Addr         string
	SyncEndpoint string
	QueuePath    string
	DataDir      string
	EnginePath   string
	AuthToken    string
}

// Run executes every check and reports whether the environment is healthy
// (no "fail" verdicts).
func Run(ctx context.Context, opts Options, out io.Writer) bool {
	checks := All(ctx, opts)
	failed := false
	for _, check := range checks {
		mark := map[string]string{"ok": "OK ", "warn": "WARN", "fail": "FAIL"}[check.Verdict]
		fmt.Fprintf(out, "[%s] %s: %s\n", mark, check.Name, check.Detail)
		if check.Verdict == "fail" {
			failed = true
		}
	}
	return !failed
}

// All runs every diagnostic and returns the results.
func All(ctx context.Context, opts Options) []Check {
	return []Check{
		checkPort(opts.Addr),
		checkOriginRules(opts.Addr),
		checkAuthToken(opts.AuthToken),
		checkQueue(opts.QueuePath),
		checkDataDir(opts.DataDir),
		checkEngine(ctx, opts.EnginePath),
		checkSyncEndpoint(ctx, opts.SyncEndpoint),
		checkVersion(),
	}
}

func checkPort(addr string) Check {
	if addr == "" {
		addr = "127.0.0.1:9911"
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		// Nothing listening is GOOD for a pre-flight check (the daemon will
		// bind); only report it neutrally.
		return Check{"port", "ok", fmt.Sprintf("%s is free (daemon can bind)", addr)}
	}
	conn.Close()
	return Check{"port", "warn", fmt.Sprintf("%s is already in use -- either the daemon is running (fine) or another process owns the port (start with -addr)", addr)}
}

func checkOriginRules(addr string) Check {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return Check{"origin", "fail", fmt.Sprintf("addr %q is not loopback: the /rpc endpoint must never be exposed beyond this machine", addr)}
	}
	return Check{"origin", "ok", "loopback binding; browsers may connect from localhost origins only"}
}

func checkAuthToken(token string) Check {
	if token == "" {
		return Check{"auth", "warn", "no -auth-token set: ANY local process can enqueue tasks and write into the sandbox (fine for dev; set a token in shared environments)"}
	}
	return Check{"auth", "ok", fmt.Sprintf("token required on /rpc (%d chars)", len(token))}
}

func checkQueue(path string) Check {
	if path == "" {
		return Check{"queue", "fail", "no queue path configured"}
	}
	q, err := queue.Open(path)
	if err != nil {
		return Check{"queue", "fail", fmt.Sprintf("queue cannot be opened: %v", err)}
	}
	tasks := q.List()
	queued, failed := 0, 0
	for _, t := range tasks {
		switch t.State {
		case "queued", "running":
			queued++
		case "failed":
			failed++
		}
	}
	detail := fmt.Sprintf("%s: %d pending, %d parked as failed", path, queued, failed)
	if failed > 0 {
		return Check{"queue", "warn", detail + " (inspect with cortex.task.list)"}
	}
	return Check{"queue", "ok", detail}
}

func checkDataDir(dir string) Check {
	if dir == "" {
		return Check{"data-dir", "fail", "no data dir configured (fs.write sandbox)"}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Check{"data-dir", "fail", fmt.Sprintf("%s cannot be created: %v", dir, err)}
	}
	probe := dir + string(os.PathSeparator) + ".doctor-probe"
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return Check{"data-dir", "fail", fmt.Sprintf("%s is not writable: %v", dir, err)}
	}
	os.Remove(probe)
	return Check{"data-dir", "ok", fmt.Sprintf("%s is writable (fs.write sandbox)", dir)}
}

func checkEngine(ctx context.Context, enginePath string) Check {
	if enginePath == "" {
		return Check{"engine", "ok", "not configured: tasks run through the built-in Go executor"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, enginePath).CombinedOutput()
	if err != nil && out == nil {
		return Check{"engine", "fail", fmt.Sprintf("%s cannot be spawned: %v", enginePath, err)}
	}
	// The CLI prints usage (and exits 2) when run without arguments.
	version := firstLine(string(out))
	if version == "" {
		version = "(no output)"
	}
	return Check{"engine", "ok", fmt.Sprintf("%s responds: %.80s", enginePath, version)}
}

func checkSyncEndpoint(ctx context.Context, endpoint string) Check {
	if endpoint == "" {
		return Check{"sync-endpoint", "warn", "no sync endpoint: queued sync tasks will fail until one is configured"}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return Check{"sync-endpoint", "fail", fmt.Sprintf("%s is not a valid URL: %v", endpoint, err)}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Check{"sync-endpoint", "warn", fmt.Sprintf("%s unreachable right now: %v (tasks queue until it is)", endpoint, err)}
	}
	resp.Body.Close()
	return Check{"sync-endpoint", "ok", fmt.Sprintf("%s answered HTTP %d", endpoint, resp.StatusCode)}
}

func checkVersion() Check {
	return Check{"version", "ok", fmt.Sprintf("cortex-daemon %s", server.Version)}
}

func firstLine(s string) string {
	for _, line := range splitLines(s) {
		if line != "" {
			return line
		}
	}
	return ""
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// JSON renders the checks for tooling (cortex-daemon doctor --json).
func JSON(checks []Check) ([]byte, error) {
	return json.Marshal(checks)
}
