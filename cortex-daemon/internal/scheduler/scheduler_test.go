// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

type recordingExecutor struct {
	tasks  []queue.Task
	errors []error
}

func (r *recordingExecutor) Execute(_ context.Context, task queue.Task) error {
	i := len(r.tasks)
	r.tasks = append(r.tasks, task)
	return r.errors[i]
}

func newFixture(t *testing.T, execErrors []error) (*queue.Queue, *recordingExecutor, *Scheduler) {
	t.Helper()
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	exec := &recordingExecutor{errors: execErrors}
	s := &Scheduler{Queue: q, Executor: exec, Connected: func(context.Context) bool { return true }, MaxAttempts: 2}
	return q, exec, s
}

func TestExecutesDueTaskAndMarksCompleted(t *testing.T) {
	q, exec, s := newFixture(t, []error{nil})
	body, _ := json.Marshal(map[string]any{"entries": []int{1}})
	task, _ := q.Enqueue("syncNotes", body, time.Time{})

	s.Tick(context.Background())

	got, _ := q.Get(task.ID)
	if got.State != queue.StateCompleted {
		t.Fatalf("want completed, got %s", got.State)
	}
	if len(exec.tasks) != 1 || exec.tasks[0].ID != task.ID {
		t.Fatalf("executor saw %+v", exec.tasks)
	}
	var decoded struct {
		Entries []int `json:"entries"`
	}
	if err := json.Unmarshal(exec.tasks[0].Body, &decoded); err != nil || len(decoded.Entries) != 1 {
		t.Fatalf("executor received wrong body: %v", exec.tasks[0].Body)
	}
}

func TestOfflineSchedulerLeavesQueueUntouched(t *testing.T) {
	q, exec, s := newFixture(t, []error{nil})
	s.Connected = func(context.Context) bool { return false }
	task, _ := q.Enqueue("syncNotes", nil, time.Time{})

	s.Tick(context.Background())

	got, _ := q.Get(task.ID)
	if got.State != queue.StateQueued {
		t.Fatalf("offline tick must not touch queued tasks, got %s", got.State)
	}
	if len(exec.tasks) != 0 {
		t.Fatalf("executor must not run while offline")
	}
}

func TestFailureRequeuesThenParksAfterMaxAttempts(t *testing.T) {
	q, exec, s := newFixture(t, []error{errors.New("down"), errors.New("down"), errors.New("down")})
	task, _ := q.Enqueue("syncNotes", nil, time.Time{})
	s.Connected = func(context.Context) bool { return true }

	// Bypass backoff between attempts by overriding RunAt for each pass.
	for range 3 {
		s.Tick(context.Background())
		got, _ := q.Get(task.ID)
		if got.State == queue.StateQueued {
			if err := q.SetRunAt(task.ID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatalf("set runAt: %v", err)
			}
		}
	}
	got, _ := q.Get(task.ID)
	if got.State != queue.StateFailed {
		t.Fatalf("task should exhaust attempts and fail, got %s (%d executor runs)", got.State, len(exec.tasks))
	}
	if len(exec.tasks) != 2 {
		t.Fatalf("expected exactly maxAttempts executor runs, got %d", len(exec.tasks))
	}
}
