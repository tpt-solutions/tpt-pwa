// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package syncexec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

func TestSyncExecutorPushesEntries(t *testing.T) {
	var bodies []map[string]any
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	body, _ := json.Marshal(syncTaskBody{Entries: []Entry{
		{ID: "n1:create", Kind: "note", Action: "create", Payload: json.RawMessage(`{"id":"n1","title":"hello"}`)},
		{ID: "n2:delete", Kind: "note", Action: "delete", Payload: json.RawMessage(`{"id":"n2"}`)},
	}})
	task := queue.Task{ID: "t1", Kind: "syncNotes", Body: body}
	exec := &SyncExecutor{Endpoint: endpoint.URL}
	if err := exec.Execute(context.Background(), task); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 pushes, got %d", len(bodies))
	}
	if bodies[0]["action"] != "create" {
		t.Fatalf("unexpected first body: %v", bodies[0])
	}
}

func TestSyncExecutorRejectsUnknownKindAndServerError(t *testing.T) {
	exec := &SyncExecutor{Endpoint: "https://invalid.example/sync"}

	if err := exec.Execute(context.Background(), queue.Task{Kind: "mystery"}); err == nil {
		t.Fatal("unknown kind must error")
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	body, _ := json.Marshal(syncTaskBody{Entries: []Entry{{ID: "x", Action: "create", Payload: json.RawMessage(`{}`)}}})
	if err := exec.Execute(context.Background(), queue.Task{Kind: "syncNotes", Body: body}); err == nil {
		t.Fatal("5xx must surface as error for retry")
	}
}
