// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// cortex-demo is the companion CLI for examples/local-sync: it plays both
// non-PWA roles of the spec §4 story so the whole loop can be demoed from a
// terminal.
//
//	cortex-demo serve-mock -addr 127.0.0.1:9999
//	    a stand-in sync endpoint that logs every pushed note and 200s.
//
//	cortex-demo sync-once -daemon 127.0.0.1:9911 -endpoint http://127.0.0.1:9999/sync
//	    connects to the daemon like the PWA would (ping -> enqueue a sample
//	    syncNotes task -> wait for cortex.event.taskCompleted -> print the
//	    final task status). It also demonstrates the optional shared-token
//	    auth via the `token` query parameter.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/coder/websocket"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch os.Args[1] {
	case "serve-mock":
		serveMock(ctx, os.Args[2:])
	case "sync-once":
		syncOnce(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cortex-demo serve-mock -addr 127.0.0.1:9999")
	fmt.Fprintln(os.Stderr, "       cortex-demo sync-once -daemon 127.0.0.1:9911 -endpoint http://127.0.0.1:9999/sync [-token t] [-title \"Hello\"]")
}

// serveMock: the stand-in sync endpoint.
func serveMock(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("serve-mock", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:9999", "listen address")
	fs.Parse(args)

	var mu sync.Mutex
	received := 0
	http.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received++
		n := received
		mu.Unlock()
		log.Printf("mock endpoint: push #%d %v", n, body)
		w.WriteHeader(http.StatusOK)
	})
	log.Printf("mock sync endpoint on http://%s/sync", *addr)
	server := &http.Server{Addr: *addr, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Shutdown(context.Background()) }()
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// syncOnce: a PWA-shaped client round trip.
func syncOnce(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("sync-once", flag.ExitOnError)
	daemon := fs.String("daemon", "127.0.0.1:9911", "daemon host:port")
	token := fs.String("token", "", "shared auth token (query param)")
	endpoint := fs.String("endpoint", "http://127.0.0.1:9999/sync", "sync endpoint the daemon will push to")
	title := fs.String("title", "Hello from cortex-demo", "sample note title")
	timeout := fs.Duration("timeout", 30*time.Second, "overall deadline")
	fs.Parse(args)

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	url := fmt.Sprintf("ws://%s/rpc", *daemon)
	if *token != "" {
		url += "?token=" + *token
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		log.Fatalf("dial %s: %v", url, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	type pending struct {
		result chan map[string]any
	}
	responses := map[int]*pending{}
	notifications := make(chan map[string]any, 8)
	var mu sync.Mutex
	readerDone := make(chan struct{})

	go func() {
		defer close(readerDone)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(data, &message) != nil {
				continue
			}
			if id, ok := message["id"].(float64); ok {
				mu.Lock()
				waiting := responses[int(id)]
				mu.Unlock()
				if waiting != nil {
					waiting.result <- message
				}
				continue
			}
			if _, ok := message["method"]; ok {
				notifications <- message
			}
		}
	}()

	call := func(method string, params map[string]any) map[string]any {
		mu.Lock()
		id := len(responses) + 1
		waiting := &pending{result: make(chan map[string]any, 1)}
		responses[id] = waiting
		mu.Unlock()
		request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			request["params"] = params
		}
		payload, _ := json.Marshal(request)
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			log.Fatalf("write %s: %v", method, err)
		}
		select {
		case response := <-waiting.result:
			return response
		case <-ctx.Done():
			log.Fatalf("%s: %v", method, ctx.Err())
			return nil
		}
	}

	pong := call("cortex.ping", nil)
	log.Printf("ping -> %v", pong["result"])

	// Path A (spec §4): hand the daemon one queued note and let it own the sync.
	params := map[string]any{
		"kind": "syncNotes",
		"entries": []map[string]any{{
			"id":     "demo-note-1:create",
			"kind":   "note",
			"action": "create",
			"payload": map[string]any{
				"id": "demo-note-1", "title": *title, "body": "created offline, synced natively",
				"createdAt": time.Now().UnixMilli(), "updatedAt": time.Now().UnixMilli(), "syncedAt": nil,
			},
			"queuedAt": time.Now().UnixMilli(),
		}},
	}
	enqueued := call("cortex.task.enqueue", params)
	result := enqueued["result"].(map[string]any)
	taskID := result["taskId"].(string)
	log.Printf("enqueued task %s (state %v); waiting for completion...", taskID, result["state"])

	for {
		select {
		case notification := <-notifications:
			if notification["method"] == "cortex.event.taskCompleted" {
				log.Printf("notification: %v", notification["params"])
				status := call("cortex.task.status", map[string]any{"taskId": taskID})
				log.Printf("final status: %v", status["result"])
				log.Printf("done — the daemon pushed the note to %s (watch the mock endpoint log)", *endpoint)
				return
			}
		case <-ctx.Done():
			log.Fatalf("timed out waiting for taskCompleted: %v", ctx.Err())
		}
	}
}
