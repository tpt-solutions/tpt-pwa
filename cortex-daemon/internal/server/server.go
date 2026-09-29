// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package server assembles the daemon's runtime from a Config: persistent
// queue, WebSocket JSON-RPC broker, connectivity-aware scheduler, and the
// fs.write sandbox. Both the CLI (cmd/cortex-daemon) and the gomobile build
// (mobile/ for Android) drive this one assembly point.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/engineexec"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/rpc"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/scheduler"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/syncexec"
)

const Version = "0.1.0"

// maxFSWriteBytes caps decoded fs.write payloads. The WebSocket read limit
// already bounds messages at 4 MiB; this rejects decoded sizes explicitly so
// the sandbox cannot be pushed into huge synchronous writes.
const maxFSWriteBytes = 4 << 20

// Config carries every knob the daemon exposes.
type Config struct {
	Addr         string
	SyncEndpoint string
	QueuePath    string
	DataDir      string
	PollEvery    time.Duration
	MaxAttempts  int
	// EnginePath optionally routes task execution through the cortex-engine
	// VM binary (spec §6 DSL) instead of the built-in Go executor.
	EnginePath   string
	EngineScript string
	// AuthToken, when set, is required on every /rpc upgrade: either as the
	// `token` query parameter (the browser-friendly path -- WebSocket APIs
	// cannot set custom headers) or an X-Cortex-Token header. Empty disables
	// the check (default: loopback-only trust, as in spec §3).
	AuthToken string
}

// Run blocks until ctx is cancelled or the listener fails.
func Run(ctx context.Context, cfg Config) error {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = 5 * time.Second
	}
	log.Printf("cortex-daemon %s starting", Version)

	taskQueue, err := queue.Open(cfg.QueuePath)
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}

	broker := rpc.NewBroker()
	sched := &scheduler.Scheduler{
		Queue:       taskQueue,
		Executor:    buildExecutor(ctx, cfg),
		Connected:   syncexec.HTTPConnectivity(cfg.SyncEndpoint, 3*time.Second),
		PollEvery:   cfg.PollEvery,
		MaxAttempts: cfg.MaxAttempts,
		OnTransition: func(taskID string, state queue.State) {
			switch state {
			case queue.StateCompleted, queue.StateFailed:
				broker.Broadcast("cortex.event.taskCompleted", map[string]any{"taskId": taskID, "state": string(state)})
			}
		},
	}
	go sched.Run(ctx)

	// NewHandler builds the HTTP surface (exported for tests and embedders).
	mux := http.NewServeMux()
	mux.Handle("/rpc", NewHandler(ctx, cfg, taskQueue, broker))

	server := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("listening on ws://%s/rpc", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// NewHandler returns the /rpc WebSocket endpoint: loopback origins allowed,
// optional shared-token auth (query `token` or X-Cortex-Token header), and a
// bounded read limit.
func NewHandler(ctx context.Context, cfg Config, taskQueue *queue.Queue, broker *rpc.Broker) http.Handler {
	dispatch := func(ctx context.Context, request rpc.Request) (any, *rpc.RPCError) {
		handler, ok := handlers(cfg.DataDir, taskQueue)[request.Method]
		if !ok {
			return nil, &rpc.RPCError{Code: rpc.CodeMethodNotFound, Message: "method not found: " + request.Method}
		}
		result, err := handler(ctx, request.Params)
		if err != nil {
			if rpcErr, ok := err.(*rpc.RPCError); ok {
				return nil, rpcErr
			}
			return nil, &rpc.RPCError{Code: rpc.CodeServerError, Message: err.Error()}
		}
		return result, nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.AuthToken != "" {
			token := r.URL.Query().Get("token")
			if token == "" {
				token = r.Header.Get("X-Cortex-Token")
			}
			// Constant-time compare: the token gates task execution and fs.
			if !constantTimeEquals(token, cfg.AuthToken) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		// Browser PWAs connect from http(s)://localhost:<port> origins; the
		// daemon is loopback-only, so any loopback origin is acceptable.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
		})
		if err != nil {
			return
		}
		conn.SetReadLimit(maxFSWriteBytes*4/3 + (1 << 20)) // base64 payload + envelope headroom
		go func() { <-ctx.Done(); conn.Close(websocket.StatusGoingAway, "daemon shutting down") }()
		broker.ServeConn(r.Context(), dispatch, conn)
	})
}

func constantTimeEquals(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// buildExecutor picks the task execution path: the cortex-engine VM when
// configured (and actually spawnable), the built-in Go executor otherwise.
func buildExecutor(ctx context.Context, cfg Config) scheduler.Executor {
	builtin := &syncexec.SyncExecutor{Endpoint: cfg.SyncEndpoint}
	if cfg.EnginePath == "" {
		return builtin
	}
	engine := &engineexec.Executor{
		EnginePath: cfg.EnginePath,
		ScriptPath: cfg.EngineScript,
		Endpoint:   cfg.SyncEndpoint,
		Connected:  syncexec.HTTPConnectivity(cfg.SyncEndpoint, 3*time.Second),
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := engine.Probe(probeCtx); err != nil {
		log.Printf("engine %q unusable (%v); falling back to built-in executor", cfg.EnginePath, err)
		return builtin
	}
	log.Printf("task execution routed through cortex-engine VM: %s", cfg.EnginePath)
	return engine
}

func handlers(dataDir string, taskQueue *queue.Queue) rpc.Handlers {
	return rpc.Handlers{
		"cortex.ping":         pingHandler(),
		"cortex.task.enqueue": taskEnqueueHandler(taskQueue),
		"cortex.task.status":  taskStatusHandler(taskQueue),
		"cortex.task.list":    taskListHandler(taskQueue),
		"fs.write":            fsWriteHandler(dataDir),
	}
}

func pingHandler() rpc.Handler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"pong": true, "version": Version}, nil
	}
}

func taskEnqueueHandler(taskQueue *queue.Queue) rpc.Handler {
	type params struct {
		Kind    string            `json:"kind"`
		Payload json.RawMessage   `json:"payload,omitempty"`
		Entries []json.RawMessage `json:"entries,omitempty"`
		RunAt   *time.Time        `json:"runAt,omitempty"`
	}
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p params
		if err := rpc.ParseParams(raw, &p); err != nil {
			return nil, err
		}
		if p.Kind == "" {
			return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: "kind is required"}
		}
		body := p.Payload
		if p.Entries != nil {
			// syncNotes-style tasks: normalize to {"entries":[...]} for the executor.
			encoded, err := json.Marshal(map[string]any{"entries": p.Entries})
			if err != nil {
				return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: "entries must be objects"}
			}
			body = encoded
		}
		var runAt time.Time
		if p.RunAt != nil {
			runAt = *p.RunAt
		}
		task, err := taskQueue.Enqueue(p.Kind, body, runAt)
		if err != nil {
			return nil, err
		}
		return map[string]any{"taskId": task.ID, "state": string(task.State)}, nil
	}
}

func taskStatusHandler(taskQueue *queue.Queue) rpc.Handler {
	type params struct {
		TaskID string `json:"taskId"`
	}
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p params
		if err := rpc.ParseParams(raw, &p); err != nil {
			return nil, err
		}
		task, ok := taskQueue.Get(p.TaskID)
		if !ok {
			return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: "unknown task " + p.TaskID}
		}
		return taskToJSON(task), nil
	}
}

func taskListHandler(taskQueue *queue.Queue) rpc.Handler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		tasks := taskQueue.List()
		out := make([]map[string]any, len(tasks))
		for i, task := range tasks {
			out[i] = taskToJSON(task)
		}
		return map[string]any{"tasks": out}, nil
	}
}

func fsWriteHandler(dataDir string) rpc.Handler {
	type params struct {
		Path   string `json:"path"`
		Buffer string `json:"buffer"` // base64
	}
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p params
		if err := rpc.ParseParams(raw, &p); err != nil {
			return nil, err
		}
		if p.Path == "" {
			return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: "path is required"}
		}
		// Sandbox: everything lands under dataDir, no traversal out of it.
		target := filepath.Join(dataDir, filepath.FromSlash(p.Path))
		root, err := filepath.Abs(dataDir)
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return nil, err
		}
		if !within(root, abs) {
			return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: "path escapes the daemon sandbox"}
		}
		data, err := base64.StdEncoding.DecodeString(p.Buffer)
		if err != nil {
			return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: "buffer must be base64"}
		}
		if len(data) > maxFSWriteBytes {
			return nil, &rpc.RPCError{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf("buffer exceeds the %d byte limit", maxFSWriteBytes)}
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return nil, err
		}
		return map[string]any{"path": abs, "bytesWritten": len(data)}, nil
	}
}

func taskToJSON(task queue.Task) map[string]any {
	return map[string]any{
		"taskId":    task.ID,
		"kind":      task.Kind,
		"state":     string(task.State),
		"attempts":  task.Attempts,
		"runAt":     task.RunAt,
		"createdAt": task.CreatedAt,
		"updatedAt": task.UpdatedAt,
		"lastError": task.LastError,
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
