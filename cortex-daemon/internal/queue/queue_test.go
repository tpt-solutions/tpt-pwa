// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package queue

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestEnqueuePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q1, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"entries": []int{1, 2, 3}})
	task, err := q1.Enqueue("syncNotes", body, time.Time{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	q2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := q2.Get(task.ID)
	if !ok {
		t.Fatalf("task %s lost across reopen", task.ID)
	}
	if got.Kind != "syncNotes" || got.State != StateQueued {
		t.Fatalf("unexpected task after reopen: %+v", got)
	}
	var decoded struct {
		Entries []int `json:"entries"`
	}
	if err := json.Unmarshal(got.Body, &decoded); err != nil || len(decoded.Entries) != 3 {
		t.Fatalf("body not preserved: %v %v", decoded, err)
	}
}

func TestRunningTaskRecoversToQueuedAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q1, _ := Open(path)
	task, _ := q1.Enqueue("syncNotes", nil, time.Time{})
	if _, err := q1.Transition(task.ID, StateRunning, "", 3, time.Now()); err != nil {
		t.Fatalf("to running: %v", err)
	}

	q2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := q2.Get(task.ID)
	if got.State != StateQueued {
		t.Fatalf("crashed running task should be requeued, got %s", got.State)
	}
}

func TestFailureRetriesWithBackoffThenParksFailed(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "queue.json"))
	task, _ := q.Enqueue("syncNotes", nil, time.Time{})
	const maxAttempts = 2
	now := time.Now()

	if _, err := q.Transition(task.ID, StateRunning, "", maxAttempts, now); err != nil {
		t.Fatalf("attempt 1 start: %v", err)
	}
	retried, err := q.Transition(task.ID, StateQueued, "endpoint down", maxAttempts, now)
	if err != nil {
		t.Fatalf("attempt 1 fail: %v", err)
	}
	if retried.State != StateQueued || retried.LastError != "endpoint down" {
		t.Fatalf("want requeued with error, got %+v", retried)
	}
	if !retried.RunAt.After(now) {
		t.Fatalf("retry must carry a backoff RunAt in the future, got %v", retried.RunAt)
	}
	if q.Due(now) != nil {
		t.Fatalf("backed-off task must not be due yet")
	}

	if _, err := q.Transition(task.ID, StateRunning, "", maxAttempts, now); err != nil {
		t.Fatalf("attempt 2 start: %v", err)
	}
	parked, err := q.Transition(task.ID, StateQueued, "endpoint down again", maxAttempts, now)
	if err != nil {
		t.Fatalf("attempt 2 fail: %v", err)
	}
	if parked.State != StateFailed {
		t.Fatalf("task should be parked as failed after %d attempts, got %s", maxAttempts, parked.State)
	}
}

func TestDueRespectsRunAt(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "queue.json"))
	now := time.Now()
	future := now.Add(time.Hour)
	if _, err := q.Enqueue("syncNotes", nil, future); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if due := q.Due(now); len(due) != 0 {
		t.Fatalf("future task must not be due, got %d", len(due))
	}
	if due := q.Due(future.Add(time.Minute)); len(due) != 1 {
		t.Fatalf("task should be due after RunAt, got %d", len(due))
	}
}

func TestRecurringTaskRequeuesAfterCompletion(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "queue.json"))
	task, _, err := q.EnqueueSpec(Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Every: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("enqueue recurring: %v", err)
	}
	if _, err := q.Transition(task.ID, StateRunning, "", 8, time.Now()); err != nil {
		t.Fatalf("to running: %v", err)
	}
	if _, err := q.Transition(task.ID, StateCompleted, "", 8, time.Now()); err != nil {
		t.Fatalf("to completed: %v", err)
	}

	before := time.Now()
	next := before.Add(task.Every)
	requeued, err := q.Requeue(task.ID, next, before)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued.State != StateQueued {
		t.Fatalf("want queued, got %s", requeued.State)
	}
	if requeued.Attempts != 0 {
		t.Fatalf("recurrence must get a fresh attempt budget, got %d", requeued.Attempts)
	}
	if want := before.Add(50 * time.Millisecond); requeued.RunAt.Before(want.Add(-time.Millisecond)) || requeued.RunAt.After(want.Add(time.Millisecond)) {
		t.Fatalf("runAt = %s, want ~%s", requeued.RunAt, want)
	}
	// The recurrence is due exactly on its interval.
	if due := q.Due(before.Add(49 * time.Millisecond)); len(due) != 0 {
		t.Fatalf("not due yet, got %d", len(due))
	}
	if due := q.Due(before.Add(51 * time.Millisecond)); len(due) != 1 {
		t.Fatalf("due after the interval, got %d", len(due))
	}

	// Only completed tasks requeue, and only with a next fire time.
	if _, err := q.Requeue(task.ID, next, time.Now()); err == nil {
		t.Fatal("requeuing a queued task must fail")
	}
	if _, err := q.Requeue(task.ID, time.Time{}, time.Now()); err == nil {
		t.Fatal("requeue needs a next fire time")
	}
}

func TestCronRecurringTaskRequeuesOnTheNextMatchingMinute(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "queue.json"))
	// Every day at 09:00 local.
	task, _, err := q.EnqueueSpec(Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Cron: "0 9 * * *"})
	if err != nil {
		t.Fatalf("enqueue cron task: %v", err)
	}
	if _, err := q.Transition(task.ID, StateRunning, "", 8, time.Now()); err != nil {
		t.Fatalf("to running: %v", err)
	}
	if _, err := q.Transition(task.ID, StateCompleted, "", 8, time.Now()); err != nil {
		t.Fatalf("to completed: %v", err)
	}

	// The scheduler asks the queue helper for the next fire time.
	after := time.Date(2026, 10, 7, 10, 30, 0, 0, time.Local)
	next := NextRunAt(*task, after)
	if want := time.Date(2026, 10, 8, 9, 0, 0, 0, time.Local); !next.Equal(want) {
		t.Fatalf("next cron fire = %s, want %s", next, want)
	}

	requeued, err := q.Requeue(task.ID, next, after)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued.Cron != "0 9 * * *" || requeued.Attempts != 0 {
		t.Fatalf("cron task must keep its expression and reset attempts: %+v", requeued)
	}
	if !requeued.RunAt.Equal(next) {
		t.Fatalf("runAt = %s, want %s", requeued.RunAt, next)
	}

	// Pruning must not cancel the schedule.
	if _, err := q.PruneFinished(); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got, ok := q.Get(task.ID); !ok {
		t.Fatal("a cron task must survive pruning")
	} else if got.State != StateQueued {
		t.Fatalf("state after prune: %s", got.State)
	}
}

func TestPruneSkipsRecurringTasks(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "queue.json"))
	oneShot, _ := q.Enqueue("syncNotes", nil, time.Time{})
	recurring, _, _ := q.EnqueueSpec(Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Every: time.Minute})
	for _, task := range []string{oneShot.ID, recurring.ID} {
		if _, err := q.Transition(task, StateRunning, "", 8, time.Now()); err != nil {
			t.Fatalf("to running: %v", err)
		}
		if _, err := q.Transition(task, StateCompleted, "", 8, time.Now()); err != nil {
			t.Fatalf("to completed: %v", err)
		}
	}

	pruned, err := q.PruneFinished()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned %d tasks, want only the one-shot", pruned)
	}
	if got, _ := q.Get(recurring.ID); got.ID != recurring.ID {
		t.Fatal("a completed recurring task must survive pruning: it is a live schedule")
	}
}

func TestCompletedRecurringTaskRecoversAfterCrash(t *testing.T) {
	// Crash window: the task completed, the requeue had not happened yet.
	path := filepath.Join(t.TempDir(), "queue.json")
	q1, _ := Open(path)
	task, _, _ := q1.EnqueueSpec(Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Every: time.Minute})
	if _, err := q1.Transition(task.ID, StateRunning, "", 8, time.Now()); err != nil {
		t.Fatalf("to running: %v", err)
	}
	if _, err := q1.Transition(task.ID, StateCompleted, "", 8, time.Now()); err != nil {
		t.Fatalf("to completed: %v", err)
	}

	q2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := q2.Get(task.ID)
	if got.State != StateQueued {
		t.Fatalf("a completed recurring task must resume its schedule after a crash, got %s", got.State)
	}
	if got.Attempts != 0 {
		t.Fatalf("recovered recurrence must start fresh, got %d attempts", got.Attempts)
	}
}
