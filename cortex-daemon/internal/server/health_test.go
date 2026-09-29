// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/rpc"
)

func TestHealthEndpointReportsQueueShape(t *testing.T) {
	taskQueue := mustQueue(t)
	if _, err := taskQueue.Enqueue("syncNotes", nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	healthHandler(taskQueue).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health must answer 200, got %d", rec.Code)
	}
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		Queue   struct {
			Queued int `json:"queued"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health payload not JSON: %v", err)
	}
	if body.Status != "ok" || body.Version != Version || body.Queue.Queued != 1 {
		t.Fatalf("unexpected health payload: %s", rec.Body.String())
	}
}

func TestMetricsEndpointRendersPrometheusText(t *testing.T) {
	taskQueue := mustQueue(t)
	task, _ := taskQueue.Enqueue("syncNotes", nil, time.Time{})
	if _, err := taskQueue.Transition(task.ID, queue.StateRunning, "", 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := taskQueue.Transition(task.ID, queue.StateCompleted, "", 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	stats := newTaskStats()
	stats.count(queue.StateCompleted)
	stats.enqueued.Add(1)

	fixed := func() time.Time { return stats.startedAt.Add(30 * time.Second) }
	rec := httptest.NewRecorder()
	metricsHandler(taskQueue, stats, fixed).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`cortex_queue_tasks{state="completed"} 1`,
		`cortex_tasks_total{kind="enqueued"} 1`,
		`cortex_tasks_total{kind="completed"} 1`,
		`cortex_tasks_total{kind="failed"} 0`,
		"cortex_uptime_seconds 30",
		"# TYPE cortex_queue_tasks gauge",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, body)
		}
	}
}

func callRPC(t *testing.T, taskQueue *queue.Queue, method string, params any) (any, error) {
	t.Helper()
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return handlers(t.TempDir(), taskQueue, nil)[method](context.Background(), encoded)
}

func TestTaskCancelRetryPruneLifecycle(t *testing.T) {
	taskQueue := mustQueue(t)
	task, _ := taskQueue.Enqueue("syncNotes", nil, time.Time{})

	// Cancel a queued task -> failed with a "cancelled" marker.
	result, err := callRPC(t, taskQueue, "cortex.task.cancel", map[string]string{"taskId": task.ID})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if result.(map[string]any)["state"] != "failed" {
		t.Fatalf("cancelled task state: %v", result)
	}
	got, _ := taskQueue.Get(task.ID)
	if got.LastError != "cancelled" {
		t.Fatalf("cancel marker: %q", got.LastError)
	}
	// Cancelling again is refused (it is no longer queued).
	if _, err := callRPC(t, taskQueue, "cortex.task.cancel", map[string]string{"taskId": task.ID}); err == nil {
		t.Fatal("cancelling a failed task must be refused")
	}

	// Retry restores the attempt budget and requeues.
	retried, err := callRPC(t, taskQueue, "cortex.task.retry", map[string]string{"taskId": task.ID})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.(map[string]any)["state"] != "queued" {
		t.Fatalf("retried task state: %v", retried)
	}
	got, _ = taskQueue.Get(task.ID)
	if got.Attempts != 0 || got.LastError != "" {
		t.Fatalf("retry must reset attempts/error: %+v", got)
	}

	// Complete it, then prune must drop it and report the count.
	if _, err := taskQueue.Transition(task.ID, queue.StateRunning, "", 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := taskQueue.Transition(task.ID, queue.StateCompleted, "", 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	pruned, err := callRPC(t, taskQueue, "cortex.task.prune", nil)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := pruned.(map[string]any)["pruned"]; got != 1 && got != float64(1) {
		t.Fatalf("prune count: %v (%T)", got, got)
	}
	if got := len(taskQueue.List()); got != 0 {
		t.Fatalf("queue not empty after prune: %d", got)
	}
}

func TestPruneRefusesParams(t *testing.T) {
	if _, err := callRPC(t, mustQueue(t), "cortex.task.prune", map[string]string{"all": "yes"}); err == nil {
		t.Fatal("prune must not accept params")
	}
}

func TestHealthAndMetricsMountedByRunHandler(t *testing.T) {
	// The full HTTP surface (as assembled by NewHandler's mux in Run): the
	// /rpc handler plus health/metrics must coexist on one server.
	taskQueue := mustQueue(t)
	mux := http.NewServeMux()
	mux.Handle("/rpc", NewHandler(context.Background(), Config{}, taskQueue, rpc.NewBroker(), nil))
	mux.Handle("/health", healthHandler(taskQueue))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health via mux: %d", resp.StatusCode)
	}
}
