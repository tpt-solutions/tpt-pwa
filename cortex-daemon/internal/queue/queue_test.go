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
