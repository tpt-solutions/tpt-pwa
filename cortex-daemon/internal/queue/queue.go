// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package queue implements the daemon's persistent task queue. Tasks survive
// restarts (atomic JSON file), are retried with capped attempts, and are
// safe for concurrent use.
package queue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is the lifecycle state of a task.
type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
)

// Task is one unit of deferred native work (spec §4 Path A).
type Task struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Body      json.RawMessage `json:"body,omitempty"`
	RunAt     time.Time       `json:"runAt,omitempty"`
	State     State           `json:"state"`
	Attempts  int             `json:"attempts"`
	LastError string          `json:"lastError,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// Queue is a file-backed FIFO of tasks with retry bookkeeping.
type Queue struct {
	mu    sync.Mutex
	path  string
	tasks []*Task
	byID  map[string]*Task
}

// Open loads (or creates) the queue persisted at path.
func Open(path string) (*Queue, error) {
	q := &Queue{path: path, byID: map[string]*Task{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: read %s: %w", path, err)
	}
	var tasks []*Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("queue: corrupt %s: %w", path, err)
	}
	// Tasks caught mid-flight by a crash go back to the queue.
	for _, t := range tasks {
		if t.State == StateRunning {
			t.State = StateQueued
		}
		q.tasks = append(q.tasks, t)
		q.byID[t.ID] = t
	}
	return q, nil
}

// Enqueue appends a task and persists the queue.
func (q *Queue) Enqueue(kind string, body json.RawMessage, runAt time.Time) (*Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now().UTC()
	if runAt.IsZero() {
		runAt = now
	} else {
		runAt = runAt.UTC()
	}
	task := &Task{
		ID:        newID(),
		Kind:      kind,
		Body:      body,
		RunAt:     runAt,
		State:     StateQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	q.tasks = append(q.tasks, task)
	q.byID[task.ID] = task
	if err := q.saveLocked(); err != nil {
		return nil, err
	}
	return task, nil
}

// Get returns a snapshot of the task with the given id.
func (q *Queue) Get(id string) (Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.byID[id]; ok {
		return *t, true
	}
	return Task{}, false
}

// List returns snapshots of every task, oldest first.
func (q *Queue) List() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Task, len(q.tasks))
	for i, t := range q.tasks {
		out[i] = *t
	}
	return out
}

// Due returns queued tasks whose RunAt has passed, in enqueue order.
func (q *Queue) Due(now time.Time) []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	var due []Task
	for _, t := range q.tasks {
		if t.State == StateQueued && !t.RunAt.After(now) {
			due = append(due, *t)
		}
	}
	return due
}

// Transition advances a task's state machine and persists. A failed task is
// requeued with exponential backoff until it hits maxAttempts, then parked as
// failed for inspection.
func (q *Queue) Transition(id string, to State, errMsg string, maxAttempts int, now time.Time) (Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[id]
	if !ok {
		return Task{}, fmt.Errorf("queue: unknown task %s", id)
	}
	switch to {
	case StateRunning:
		if t.State != StateQueued {
			return Task{}, fmt.Errorf("queue: task %s in state %s, want queued", id, t.State)
		}
		t.Attempts++
		t.LastError = "" // fresh attempt
	case StateCompleted:
		t.LastError = errMsg
	case StateFailed:
		t.LastError = errMsg
	case StateQueued:
		// retry path: record the failure and schedule with exponential backoff
		t.LastError = errMsg
		if maxAttempts <= 0 || t.Attempts < maxAttempts {
			backoff := time.Duration(1<<min(t.Attempts, 6)) * time.Second
			t.RunAt = now.UTC().Add(backoff)
		} else {
			to = StateFailed
		}
	}
	t.State = to
	t.UpdatedAt = now.UTC()
	if err := q.saveLocked(); err != nil {
		return Task{}, err
	}
	return *t, nil
}

// SetRunAt overrides when a queued task becomes due (used by tests and
// administrative rescheduling).
func (q *Queue) SetRunAt(id string, runAt time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[id]
	if !ok {
		return fmt.Errorf("queue: unknown task %s", id)
	}
	t.RunAt = runAt.UTC()
	t.UpdatedAt = time.Now().UTC()
	return q.saveLocked()
}

// saveLocked writes the queue atomically (temp file + rename). Caller holds mu.
func (q *Queue) saveLocked() error {
	data, err := json.MarshalIndent(q.tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("queue: encode: %w", err)
	}
	dir := filepath.Dir(q.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("queue: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(q.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("queue: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("queue: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("queue: close: %w", err)
	}
	if err := os.Rename(tmpName, q.path); err != nil {
		return fmt.Errorf("queue: rename: %w", err)
	}
	return nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on the supported platforms; fall back to time.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
