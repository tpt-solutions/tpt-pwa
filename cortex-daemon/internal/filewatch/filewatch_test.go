// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package filewatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

func setup(t *testing.T) (*queue.Queue, string) {
	t.Helper()
	dataDir := t.TempDir()
	tasks, err := queue.Open(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	return tasks, dataDir
}

// TestLoopWakesMatchingTasks is the end-to-end contract: a watch task sleeps
// until a file matching its glob appears, then becomes due.
func TestLoopWakesMatchingTasks(t *testing.T) {
	tasks, dataDir := setup(t)
	spec := queue.Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Watch: "inbox/*.csv"}
	task, _, err := tasks.EnqueueSpec(spec)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Loop(ctx, tasks, dataDir, 100*time.Millisecond, slog.Default())

	// The task must not be due while asleep.
	if due := tasks.Due(time.Now()); len(due) != 0 {
		t.Fatalf("asleep watch task must not be due: %v", due)
	}

	// The inbox may not exist yet: create it AFTER the watcher is running --
	// the resync loop must pick the directory up.
	if err := os.MkdirAll(filepath.Join(dataDir, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // one resync tick

	if err := os.WriteFile(filepath.Join(dataDir, "inbox", "data.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if due := tasks.Due(time.Now()); len(due) == 1 && due[0].ID == task.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the matching file never woke the task")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLoopIgnoresNonMatchingEvents: only the glob's names wake the task.
func TestLoopIgnoresNonMatchingEvents(t *testing.T) {
	tasks, dataDir := setup(t)
	task, _, err := tasks.EnqueueSpec(queue.Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Watch: "inbox/*.csv"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Loop(ctx, tasks, dataDir, 100*time.Millisecond, slog.Default())

	if err := os.MkdirAll(filepath.Join(dataDir, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dataDir, "inbox", "data.txt"), []byte("wrong extension"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "stray.csv"), []byte("wrong directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)

	if due := tasks.Due(time.Now()); len(due) != 0 {
		t.Fatalf("non-matching events must not wake the task: %v", due)
	}

	// The matching event still works after the noise.
	if err := os.WriteFile(filepath.Join(dataDir, "inbox", "data.csv"), []byte("a,b"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if due := tasks.Due(time.Now()); len(due) == 1 && due[0].ID == task.ID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the matching file never woke the task")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A task woken and run (completed) goes back to sleep until the next event:
// watch tasks are recurring through internal/queue's requeue path.
func TestWatchTaskRequeuesAsleepAfterRun(t *testing.T) {
	tasks, _ := setup(t)
	task, _, err := tasks.EnqueueSpec(queue.Spec{Kind: "syncNotes", Body: json.RawMessage(`{}`), Watch: "inbox/*.csv"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := tasks.Transition(task.ID, queue.StateRunning, "", 8, time.Now()); err != nil {
		t.Fatalf("to running: %v", err)
	}
	completed, err := tasks.Transition(task.ID, queue.StateCompleted, "", 8, time.Now())
	if err != nil {
		t.Fatalf("to completed: %v", err)
	}

	now := time.Now()
	next := queue.NextRunAt(completed, now)
	if next.Before(now.Add(queue.WatchSleepUntil - 24*time.Hour)) {
		t.Fatalf("a run watch task must go back to sleep, got RunAt %s", next)
	}
	if !completed.IsRecurring() {
		t.Fatal("watch tasks are recurring: pruning must skip them")
	}
}

func TestResolveContainsPatternsInTheDataDir(t *testing.T) {
	dataDir := filepath.Join(string(filepath.Separator), "tmp", "cortex-data")
	cases := []struct {
		pattern string
		ok      bool
	}{
		{"inbox/*.csv", true},
		{"*.csv", true},
		{"a/b/c/*.txt", true},
		{"../escape.csv", false},
		{"a/../../escape.csv", false},
	}
	for _, tc := range cases {
		if _, ok := resolve(dataDir, tc.pattern); ok != tc.ok {
			t.Fatalf("resolve(%q) ok = %v, want %v", tc.pattern, ok, tc.ok)
		}
	}
}

func TestMatchEventScope(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "tmp", "cortex-data", "inbox")
	w := watch{pattern: "inbox/*.csv", dir: dir, glob: "*.csv"}
	if !matchEvent(w, filepath.Join(dir, "a.csv")) {
		t.Fatal("a matching file must wake")
	}
	if matchEvent(w, filepath.Join(dir, "a.txt")) {
		t.Fatal("wrong extension must not wake")
	}
	if matchEvent(w, dir) {
		t.Fatal("the directory itself must not wake")
	}
	if matchEvent(w, filepath.Join(dir, "sub", "a.csv")) {
		t.Fatal("deeper paths must not wake (non-recursive)")
	}
	if matchEvent(w, filepath.Join(filepath.Dir(dir), "a.csv")) {
		t.Fatal("outside the watched dir must not wake")
	}
}
