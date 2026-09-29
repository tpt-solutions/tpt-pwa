// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCorruptQueueFileIsMovedAsideNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	if err := os.WriteFile(path, []byte("{not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := Open(path)
	if err != nil {
		t.Fatalf("Open with a corrupt file must start fresh, got: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	moved := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "queue.json.corrupt-") {
			moved = true
		}
	}
	if !moved {
		t.Fatal("corrupt queue file was not moved aside for inspection")
	}
	task, err := q.Enqueue("syncNotes", json.RawMessage(`{}`), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := q.Get(task.ID); !ok || got.ID != task.ID {
		t.Fatal("fresh queue after corrupt-aside does not work")
	}
}

func TestEnqueueIdempotentDeduplicatesByKey(t *testing.T) {
	q, err := Open(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, dup1, err := q.EnqueueIdempotent("syncNotes", "batch-1", json.RawMessage(`{"entries":[]}`), time.Time{})
	if err != nil || dup1 {
		t.Fatalf("first submission: deduplicated=%v err=%v", dup1, err)
	}
	second, dup2, err := q.EnqueueIdempotent("syncNotes", "batch-1", json.RawMessage(`{"entries":[]}`), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !dup2 {
		t.Fatal("same key submitted twice must be reported as deduplicated")
	}
	if first.ID != second.ID {
		t.Fatalf("dedup returned a different task: %s vs %s", first.ID, second.ID)
	}
	if got := len(q.List()); got != 1 {
		t.Fatalf("queue holds %d tasks, want 1", got)
	}
	// A completed task no longer absorbs the key: a genuine resubmission runs.
	if _, err := q.Transition(first.ID, StateRunning, "", 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Transition(first.ID, StateCompleted, "", 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	third, dup3, err := q.EnqueueIdempotent("syncNotes", "batch-1", json.RawMessage(`{}`), time.Time{})
	if err != nil || dup3 {
		t.Fatalf("resubmission after completion: deduplicated=%v err=%v", dup3, err)
	}
	if third.ID == first.ID {
		t.Fatal("resubmission reused the completed task")
	}
}

func TestFinishedTasksArePrunedBeyondInspectionCap(t *testing.T) {
	q, err := Open(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Enqueue more finished tasks than the cap, with a pending one in the
	// middle: pruning must never touch pending work.
	pendingID := ""
	for i := 0; i < maxFinishedTasks+10; i++ {
		task, err := q.Enqueue("syncNotes", json.RawMessage(`{}`), time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 5 {
			pendingID = task.ID // stays queued the whole time
			continue
		}
		if _, err := q.Transition(task.ID, StateRunning, "", 8, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Transition(task.ID, StateCompleted, "", 8, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	tasks := q.List()
	finished := 0
	pendingKept := false
	for _, task := range tasks {
		if task.State == StateCompleted || task.State == StateFailed {
			finished++
		}
		if task.ID == pendingID {
			pendingKept = true
		}
	}
	if finished != maxFinishedTasks {
		t.Fatalf("kept %d finished tasks, want the cap of %d", finished, maxFinishedTasks)
	}
	if !pendingKept {
		t.Fatal("pruning dropped a pending task")
	}
	// And the pruned file still round-trips.
	q2, err := Open(q.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(q2.List()); got != len(tasks) {
		t.Fatalf("reopened queue holds %d tasks, want %d", got, len(tasks))
	}
}

func TestEnqueueRollsBackMemoryWhenSaveFails(t *testing.T) {
	dir := t.TempDir()
	// Put the queue file's parent behind a regular FILE: the save's mkdir
	// then fails deterministically on every platform (chmod is ignored on
	// Windows).
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := Open(filepath.Join(blocker, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue("syncNotes", json.RawMessage(`{}`), time.Time{}); err == nil {
		t.Fatal("enqueue should fail when persistence fails")
	}
	if got := len(q.List()); got != 0 {
		t.Fatalf("in-memory queue kept %d tasks after a failed save, want 0", got)
	}
}

func TestTransitionRollsBackMemoryWhenSaveFails(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := q.Enqueue("syncNotes", json.RawMessage(`{}`), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Break persistence by replacing the queue file's parent with a file.
	blocker := filepath.Join(dir, "sub")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	q.path = filepath.Join(blocker, "queue.json")
	q.mu.Unlock()

	if _, err := q.Transition(task.ID, StateRunning, "", 8, time.Now()); err == nil {
		t.Fatal("transition should fail when persistence fails")
	}
	snapshot, ok := q.Get(task.ID)
	if !ok || snapshot.State != StateQueued || snapshot.Attempts != 0 {
		t.Fatalf("in-memory task not rolled back: %+v", snapshot)
	}
}
