// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

// taskStats are the daemon-wide counters behind /metrics. Enqueues count
// through the enqueue handler; terminal states count through the scheduler's
// OnTransition.
type taskStats struct {
	enqueued  atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
	startedAt time.Time
}

func newTaskStats() *taskStats {
	return &taskStats{startedAt: time.Now()}
}

func (s *taskStats) count(state queue.State) {
	switch state {
	case queue.StateCompleted:
		s.completed.Add(1)
	case queue.StateFailed:
		s.failed.Add(1)
	}
}

// taskQueueJSON summarizes queue state for /health.
func taskQueueJSON(taskQueue *queue.Queue) map[string]any {
	queued, running, completed, failed := 0, 0, 0, 0
	for _, task := range taskQueue.List() {
		switch task.State {
		case queue.StateQueued:
			queued++
		case queue.StateRunning:
			running++
		case queue.StateCompleted:
			completed++
		case queue.StateFailed:
			failed++
		}
	}
	return map[string]any{
		"queued": queued, "running": running, "completed": completed, "failed": failed,
	}
}

// healthHandler answers liveness probes: 200 with identity + queue shape.
func healthHandler(taskQueue *queue.Queue) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"version": Version,
			"queue":   taskQueueJSON(taskQueue),
		})
	})
}

// metricsHandler renders Prometheus text format (no client_* instrumentation
// on purpose: the daemon has exactly five series worth caring about).
func metricsHandler(taskQueue *queue.Queue, stats *taskStats, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain; version=0.0.4")
		shape := taskQueueJSON(taskQueue)
		fmt.Fprintf(w, "# HELP cortex_queue_tasks Tasks in the persistent queue by state.\n")
		fmt.Fprintf(w, "# TYPE cortex_queue_tasks gauge\n")
		for _, state := range []string{"queued", "running", "completed", "failed"} {
			fmt.Fprintf(w, "cortex_queue_tasks{state=%q} %d\n", state, shape[state])
		}
		fmt.Fprintf(w, "# HELP cortex_tasks_total Task transitions since daemon start.\n")
		fmt.Fprintf(w, "# TYPE cortex_tasks_total counter\n")
		fmt.Fprintf(w, "cortex_tasks_total{kind=\"enqueued\"} %d\n", stats.enqueued.Load())
		fmt.Fprintf(w, "cortex_tasks_total{kind=\"completed\"} %d\n", stats.completed.Load())
		fmt.Fprintf(w, "cortex_tasks_total{kind=\"failed\"} %d\n", stats.failed.Load())
		fmt.Fprintf(w, "# HELP cortex_uptime_seconds Seconds since the daemon started.\n")
		fmt.Fprintf(w, "# TYPE cortex_uptime_seconds gauge\n")
		fmt.Fprintf(w, "cortex_uptime_seconds %d\n", int64(now().Sub(stats.startedAt).Seconds()))
	})
}
