// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// cortex-daemon is the tpt-cortex native companion's IPC host (spec §3
// Layer 3): a WebSocket JSON-RPC server on the loopback, a persistent task
// queue, and a connectivity-aware scheduler. The PWA talks to it per
// docs/jsonrpc-contract.md.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/rpc"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/scheduler"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/syncexec"
)

const version = "0.1.0"

func main() {
	addr := flag.String("addr", "127.0.0.1:9911", "listen address (loopback only by design)")
	syncEndpoint := flag.String("sync-endpoint", "https://api.tpt/sync", "remote sync endpoint")
	poll := flag.Duration("poll", 5*time.Second, "scheduler poll interval")
	maxAttempts := flag.Int("max-attempts", 8, "max task attempts before parking as failed")
	queuePath := flag.String("queue", defaultQueuePath(), "persistent task queue file")
	dataDir := flag.String("data-dir", defaultDataDir(), "sandbox root for fs.write")
	flag.Parse()
	if *maxAttempts <= 0 {
		*maxAttempts = 1
	}

	log.SetFlags(log.LstdFlags | log.LUTC)
	log.Printf("cortex-daemon %s starting", version)

	taskQueue, err := queue.Open(*queuePath)
	if err != nil {
		log.Fatalf("open queue: %v", err)
	}

	broker := rpc.NewBroker()

	sched := &scheduler.Scheduler{
		Queue:       taskQueue,
		Executor:    &syncexec.SyncExecutor{Endpoint: *syncEndpoint},
		Connected:   syncexec.HTTPConnectivity(*syncEndpoint, 3*time.Second),
		PollEvery:   *poll,
		MaxAttempts: *maxAttempts,
		OnTransition: func(taskID string, state queue.State) {
			switch state {
			case queue.StateCompleted, queue.StateFailed:
				broker.Broadcast("cortex.event.taskCompleted", map[string]any{"taskId": taskID, "state": string(state)})
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	handlers := rpc.Handlers{
		"cortex.ping":         pingHandler(),
		"cortex.task.enqueue": taskEnqueueHandler(taskQueue),
		"cortex.task.status":  taskStatusHandler(taskQueue),
		"cortex.task.list":    taskListHandler(taskQueue),
		"fs.write":            fsWriteHandler(*dataDir),
	}
	dispatch := func(ctx context.Context, request rpc.Request) (any, *rpc.RPCError) {
		handler, ok := handlers[request.Method]
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

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		// Browser PWAs connect from http(s)://localhost:<port> origins; the
		// daemon is loopback-only, so any loopback origin is acceptable.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "127.0.0.0/8:*"},
		})
		if err != nil {
			return
		}
		conn.SetReadLimit(4 << 20) // generous cap for CRDT binaries
		go func() { <-ctx.Done(); conn.Close(websocket.StatusGoingAway, "daemon shutting down") }()
		broker.ServeConn(r.Context(), dispatch, conn)
	})

	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on ws://%s/rpc", *addr)
	log.Fatal(server.ListenAndServe())
}

func pingHandler() rpc.Handler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"pong": true, "version": version}, nil
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
