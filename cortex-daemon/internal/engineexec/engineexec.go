// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package engineexec routes task execution through the cortex-engine VM
// (spec §6): the Rust binary runs the DSL script and this package plays the
// "host" side of the stdio protocol (src/host.rs in cortex-engine), serving
// the script's native.* calls with real daemon effects:
//
//	db.query          -> the task's outbox entries, one row each
//	                     (plus the configured sync endpoint per row)
//	db.exec           -> acknowledged (bookkeeping count)
//	net.isConnected   -> the scheduler's connectivity probe
//	http.post         -> a real HTTP POST from the daemon process
//
// If the engine binary is missing or dies mid-task, Execute returns an error
// and the scheduler retries with backoff.
package engineexec

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

//go:embed sync.ctx
var embeddedScript []byte

type protocolRequest struct {
	ID     uint64            `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type protocolResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Connectivity mirrors scheduler.Connectivity.
type Connectivity func(ctx context.Context) bool

// Executor implements scheduler.Executor by delegating to the engine VM.
type Executor struct {
	// EnginePath is the cortex-engine binary; required.
	EnginePath string
	// ScriptPath optionally overrides the DSL script (defaults to the
	// embedded sync.ctx).
	ScriptPath string
	// Endpoint is injected into each row as `endpoint` and used by Probe.
	Endpoint string
	// Connected reports network availability for net.isConnected.
	Connected Connectivity
	// Client performs http.post calls; defaults to http.DefaultClient.
	Client *http.Client
}

// Probe verifies the engine binary can be spawned (cheap exec smoke test).
func (e *Executor) Probe(ctx context.Context) error {
	probe := exec.CommandContext(ctx, e.EnginePath, "exec-host", "--script", filepath.Join(os.TempDir(), "cortex-engine-probe-missing.ctx"))
	err := probe.Run()
	// Exit code 2 = the CLI rejected the missing script: binary works.
	if err == nil {
		return errors.New("engine probe exited 0 for a missing script: not the cortex-engine CLI")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		return nil
	}
	return fmt.Errorf("spawn %s: %w", e.EnginePath, err)
}

// Execute implements scheduler.Executor.
func (e *Executor) Execute(ctx context.Context, task queue.Task) error {
	if e.EnginePath == "" {
		return errors.New("engineexec: no engine binary configured")
	}
	script, cleanup, err := e.scriptFile()
	if err != nil {
		return err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, e.EnginePath, "exec-host", "--script", script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("engine stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("engine stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	decoder := json.NewDecoder(stdout)
	for {
		var request protocolRequest
		if err := decoder.Decode(&request); err != nil {
			// Kill BEFORE waiting: a protocol error usually means the engine
			// is wedged (or produced garbage while still alive), and Wait
			// blocks until the process exits -- waiting first would deadlock
			// this goroutine until the task timeout.
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitErr := cmd.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) && waitErr == nil {
				return nil // engine finished the script cleanly
			}
			return fmt.Errorf("engine protocol: %w (exit: %v, stderr: %s)", err, waitErr, stderr.String())
		}
		response := e.respond(ctx, task, request)
		payload, err := json.Marshal(response)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("encode response: %w", err)
		}
		if _, err := fmt.Fprintln(stdin, string(payload)); err != nil {
			// A write failure means the engine is gone (broken pipe) or the
			// task was cancelled; make sure it is reaped either way.
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitErr := cmd.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("write to engine: %w (exit: %v)", err, waitErr)
		}
	}
}

// respond serves one native call from the script.
func (e *Executor) respond(ctx context.Context, task queue.Task, request protocolRequest) protocolResponse {
	result, err := e.handle(ctx, task, request)
	if err != nil {
		return protocolResponse{ID: request.ID, Error: err.Error()}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return protocolResponse{ID: request.ID, Error: fmt.Sprintf("encode result: %v", err)}
	}
	return protocolResponse{ID: request.ID, Result: encoded}
}

func (e *Executor) handle(ctx context.Context, task queue.Task, request protocolRequest) (any, error) {
	switch request.Method {
	case "db.query":
		return e.rowsFor(task), nil
	case "db.exec":
		// Row mutations are bookkeeping for the daemon (the outbox entry is
		// cleared when the whole task completes); acknowledge success.
		return 1, nil
	case "net.isConnected":
		if e.Connected == nil {
			return true, nil
		}
		return e.Connected(ctx), nil
	case "http.post":
		if len(request.Params) < 2 {
			return nil, errors.New("http.post needs url and body")
		}
		var url string
		if err := json.Unmarshal(request.Params[0], &url); err != nil {
			return nil, fmt.Errorf("http.post url: %w", err)
		}
		status, err := e.post(ctx, url, request.Params[1])
		if err != nil {
			return nil, err
		}
		return status, nil
	default:
		return nil, fmt.Errorf("unknown native %q", request.Method)
	}
}

// post performs the script's HTTP effect from the daemon process. The body
// is the row envelope ({id, action, payload, endpoint}); the sync endpoint
// contract accepts it like the browser fallback's {action, note} shape —
// docs/jsonrpc-contract.md documents the canonical server behavior.
func (e *Executor) post(ctx context.Context, url string, body json.RawMessage) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	client := e.Client
	if client == nil {
		// Bounded default: an unbounded client lets one stalled endpoint
		// request occupy a scheduler slot until the task timeout.
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("sync endpoint returned HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// rowsFor exposes the task's outbox entries as script rows; `endpoint` is
// injected so the script can target the daemon's configured sync endpoint.
func (e *Executor) rowsFor(task queue.Task) []map[string]any {
	var body struct {
		Entries []struct {
			ID       string          `json:"id"`
			Kind     string          `json:"kind"`
			Action   string          `json:"action"`
			Payload  json.RawMessage `json:"payload"`
			QueuedAt int64           `json:"queuedAt"`
		} `json:"entries"`
	}
	if len(task.Body) > 0 {
		if err := json.Unmarshal(task.Body, &body); err != nil {
			body.Entries = nil
		}
	}
	rows := make([]map[string]any, 0, len(body.Entries))
	for _, entry := range body.Entries {
		rows = append(rows, map[string]any{
			"id":       entry.ID,
			"kind":     entry.Kind,
			"action":   entry.Action,
			"payload":  json.RawMessage(entry.Payload),
			"queuedAt": entry.QueuedAt,
			"endpoint": e.Endpoint,
		})
	}
	return rows
}

// scriptFile materializes the DSL script (embedded by default).
func (e *Executor) scriptFile() (string, func(), error) {
	if e.ScriptPath != "" {
		return e.ScriptPath, func() {}, nil
	}
	tmp, err := os.CreateTemp("", "cortex-sync-*.ctx")
	if err != nil {
		return "", func() {}, fmt.Errorf("temp script: %w", err)
	}
	if _, err := tmp.Write(embeddedScript); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", func() {}, fmt.Errorf("temp script: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", func() {}, fmt.Errorf("temp script: %w", err)
	}
	path := tmp.Name()
	return path, func() { os.Remove(path) }, nil
}
