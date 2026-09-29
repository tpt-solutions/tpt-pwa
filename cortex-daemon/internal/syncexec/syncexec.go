// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package syncexec executes "syncNotes" tasks: it pushes queued note
// mutations to the sync endpoint. This is the native twin of the PWA's
// browser-side fallback flush (spec §4), running without any tab open.
//
// Plan (tracked in TODO.md): route execution through cortex-engine's DSL VM
// (examples/sync.ctx from spec §6) once the engine graduates from skeleton;
// the Executor interface here is the seam where it plugs in.
package syncexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

// Entry mirrors the PWA's sync outbox shape (docs/jsonrpc-contract.md).
type Entry struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

type syncTaskBody struct {
	Entries []Entry `json:"entries"`
}

// SyncExecutor pushes syncNotes payloads to the sync endpoint.
type SyncExecutor struct {
	Endpoint string
	Client   *http.Client
}

// defaultHTTPTimeout bounds every push attempt so one stalled endpoint
// request cannot occupy a scheduler slot until its task timeout.
const defaultHTTPTimeout = 30 * time.Second

// Execute implements scheduler.Executor.
func (e *SyncExecutor) Execute(ctx context.Context, task queue.Task) error {
	if task.Kind != "syncNotes" {
		return &queue.PermanentError{Err: fmt.Errorf("unsupported task kind %q", task.Kind)}
	}
	var parsed syncTaskBody
	if len(task.Body) == 0 {
		return &queue.PermanentError{Err: fmt.Errorf("task carries no body")}
	}
	if err := json.Unmarshal(task.Body, &parsed); err != nil {
		return &queue.PermanentError{Err: fmt.Errorf("parse task body: %w", err)}
	}
	if len(parsed.Entries) == 0 {
		return &queue.PermanentError{Err: fmt.Errorf("task carries no entries")}
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	var lastErr error
	pushed := 0
	for _, entry := range parsed.Entries {
		// The entry id rides along (docs/jsonrpc-contract.md) so the server
		// side can deduplicate retried pushes of the same outbox entry.
		payload, err := json.Marshal(map[string]any{"id": entry.ID, "action": entry.Action, "note": entry.Payload})
		if err != nil {
			return fmt.Errorf("encode entry %s: %w", entry.ID, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return &queue.PermanentError{Err: fmt.Errorf("build request: %w", err)}
		}
		req.Header.Set("content-type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			break // transport-level failure: stop; the scheduler will retry
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			resp.Body.Close()
			if permanentHTTP(resp.StatusCode) {
				// 4xx (except 408/429) means this entry can never succeed;
				// retrying would just burn attempts.
				return &queue.PermanentError{Err: fmt.Errorf("sync endpoint rejected entry %s with HTTP %d", entry.ID, resp.StatusCode)}
			}
			lastErr = fmt.Errorf("sync endpoint returned HTTP %d", resp.StatusCode)
			break
		}
		resp.Body.Close()
		pushed++
	}
	if pushed < len(parsed.Entries) && lastErr != nil {
		return fmt.Errorf("pushed %d/%d entries: %w", pushed, len(parsed.Entries), lastErr)
	}
	return nil
}

// permanentHTTP reports whether a status means "this entry will never be
// accepted" (client error, excluding request-timeout and rate-limit).
func permanentHTTP(status int) bool {
	return status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

// HTTPConnectivity probes the sync endpoint with a short HEAD request; any
// response (even 4xx/5xx) proves the network path is up. It serves as the
// scheduler's Connectivity check.
func HTTPConnectivity(endpoint string, timeout time.Duration) func(ctx context.Context) bool {
	return func(ctx context.Context) bool {
		head, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
		if err != nil {
			return false
		}
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(head)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}
}
