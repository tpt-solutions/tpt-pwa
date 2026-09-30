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

// A hung executor must not hold the serial queue: the per-task deadline
// cancels its context and the task is retried later (timeout = transient).
func TestTaskTimeoutBoundsOneExecution(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := q.Enqueue("syncNotes", json.RawMessage(`{}`), time.Time{})
	hung := &hungExecutor{started: make(chan struct{})}
	s := &Scheduler{
		Queue:       q,
		Executor:    hung,
		Connected:   func(context.Context) bool { return true },
		MaxAttempts: 3,
		TaskTimeout: 25 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() { s.Tick(context.Background()); close(done) }()
	<-hung.started
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tick did not return despite the task timeout")
	}
	got, _ := q.Get(task.ID)
	if got.State != queue.StateQueued || got.Attempts != 1 {
		t.Fatalf("timed-out task should be requeued with 1 attempt, got %s/%d", got.State, got.Attempts)
	}
}

type hungExecutor struct{ started chan struct{} }

func (h *hungExecutor) Execute(ctx context.Context, _ queue.Task) error {
	close(h.started)
	<-ctx.Done() // block until the scheduler's deadline cancels us
	return ctx.Err()
}

// A PermanentError parks the task as failed immediately, without burning
// retry attempts.
func TestPermanentFailureParksTaskWithoutRetries(t *testing.T) {
	q, _, s := newFixture(t, []error{&queue.PermanentError{Err: errors.New("endpoint rejected entry with HTTP 422")}})
	body, _ := json.Marshal(map[string]any{"entries": []int{1}})
	task, _ := q.Enqueue("syncNotes", body, time.Time{})

	s.Tick(context.Background())

	got, _ := q.Get(task.ID)
	if got.State != queue.StateFailed {
		t.Fatalf("permanent failure should park as failed, got %s", got.State)
	}
	if got.Attempts != 1 {
		t.Fatalf("permanent failure must not retry, got %d attempts", got.Attempts)
	}
}

func TestRecurringTaskRunsRepeatedlyUntilCancelled(t *testing.T) {
	q, exec, s := newFixture(t, []error{nil, nil})
	body, _ := json.Marshal(map[string]any{"entries": []int{1}})
	// One enqueue, 30ms interval: two Ticks separated by the interval must
	// run it twice with a fresh attempt budget each time.
	task, _, err := q.EnqueueSpec(queue.Spec{Kind: "syncNotes", Body: body, Every: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("enqueue recurring: %v", err)
	}

	s.Tick(context.Background()) // run 1 -> completed -> requeued
	got, _ := q.Get(task.ID)
	if got.State != queue.StateQueued {
		t.Fatalf("after run 1 the schedule must requeue, got %s", got.State)
	}
	if got.Attempts != 0 {
		t.Fatalf("recurrence must reset attempts, got %d", got.Attempts)
	}

	// Not due yet: the interval is respected.
	time.Sleep(10 * time.Millisecond)
	s.Tick(context.Background())
	if len(exec.tasks) != 1 {
		t.Fatalf("task ran %d times before its interval elapsed", len(exec.tasks))
	}

	time.Sleep(30 * time.Millisecond)
	s.Tick(context.Background()) // run 2 -> completed -> requeued again
	if len(exec.tasks) != 2 {
		t.Fatalf("task ran %d times, want 2", len(exec.tasks))
	}
	got, _ = q.Get(task.ID)
	if got.State != queue.StateQueued {
		t.Fatalf("after run 2 the schedule must requeue, got %s", got.State)
	}

	// Cancelling stops the schedule for good.
	if _, err := q.Cancel(task.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	s.Tick(context.Background())
	if len(exec.tasks) != 2 {
		t.Fatalf("a cancelled recurring task must never run again, ran %d times", len(exec.tasks))
	}
}

func TestRecurringTaskThatFailsPermanentlyStops(t *testing.T) {
	permanent := &queue.PermanentError{Err: errors.New("endpoint rejected the batch")}
	q, exec, s := newFixture(t, []error{permanent})
	body, _ := json.Marshal(map[string]any{"entries": []int{1}})
	task, _, err := q.EnqueueSpec(queue.Spec{Kind: "syncNotes", Body: body, Every: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("enqueue recurring: %v", err)
	}

	s.Tick(context.Background()) // fails permanently -> parked failed
	got, _ := q.Get(task.ID)
	if got.State != queue.StateFailed {
		t.Fatalf("a permanently failing recurring task must park, got %s", got.State)
	}
	time.Sleep(15 * time.Millisecond)
	s.Tick(context.Background())
	if len(exec.tasks) != 1 {
		t.Fatalf("a parked recurring task must not run again, ran %d times", len(exec.tasks))
	}
}

func TestCronTaskRequeuesToTheNextMatchingMinute(t *testing.T) {
	q, exec, s := newFixture(t, []error{nil, nil})
	body, _ := json.Marshal(map[string]any{"entries": []int{1}})
	// Every minute, on the minute.
	task, _, err := q.EnqueueSpec(queue.Spec{Kind: "syncNotes", Body: body, Cron: "* * * * *"})
	if err != nil {
		t.Fatalf("enqueue cron task: %v", err)
	}

	s.Tick(context.Background()) // run 1 -> completed -> requeued to the next minute
	if len(exec.tasks) != 1 {
		t.Fatalf("task ran %d times", len(exec.tasks))
	}
	got, _ := q.Get(task.ID)
	if got.State != queue.StateQueued {
		t.Fatalf("after run 1 the schedule must requeue, got %s", got.State)
	}
	// The requeue time is the next whole minute boundary, strictly in the future.
	if got.RunAt.Minute() == time.Now().Minute() && got.RunAt.Before(time.Now()) {
		t.Fatalf("runAt %s is not a future minute boundary", got.RunAt)
	}

	// Simulate the minute elapsing (deterministically): pull RunAt into the
	// past and tick again.
	if err := q.SetRunAt(task.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("set runAt: %v", err)
	}
	s.Tick(context.Background()) // run 2 -> requeued to the next minute again
	if len(exec.tasks) != 2 {
		t.Fatalf("task ran %d times, want 2", len(exec.tasks))
	}
	got, _ = q.Get(task.ID)
	if got.Cron != "* * * * *" || got.State != queue.StateQueued {
		t.Fatalf("the schedule must keep recurring with its expression: %+v", got)
	}
}
