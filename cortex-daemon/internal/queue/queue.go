// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package queue implements the daemon's persistent task queue. Tasks survive
// restarts (atomic JSON file, fsynced before rename), are retried with capped
// attempts, and are safe for concurrent use.
package queue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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

// maxFinishedTasks caps how many completed/failed tasks are retained for
// inspection; the rest are pruned on save so the file cannot grow forever.
const maxFinishedTasks = 100

// Task is one unit of deferred native work (spec §4 Path A).
type Task struct {
	ID string `json:"id"`
	// IdempotencyKey deduplicates re-submissions of the same logical batch
	// (e.g. a PWA retry after a lost response). Empty = no dedup.
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Kind           string          `json:"kind"`
	Body           json.RawMessage `json:"body,omitempty"`
	RunAt          time.Time       `json:"runAt,omitempty"`
	// Every, when > 0, makes the task RECUR: after each successful
	// completion the queue requeues it (fresh attempt budget) with
	// RunAt = now + Every, so recipes like periodic-fetch run on an
	// interval without anything re-enqueueing them. Failures follow the
	// normal retry/backoff/park path; a parked or cancelled recurring task
	// stops recurring.
	Every     time.Duration `json:"every,omitempty"`
	State     State         `json:"state"`
	Attempts  int           `json:"attempts"`
	LastError string        `json:"lastError,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// PermanentError wraps a task failure that retrying can never fix (e.g. the
// sync endpoint rejected an entry with a 4xx). The scheduler parks such tasks
// as failed immediately instead of burning retry attempts.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Queue is a file-backed FIFO of tasks with retry bookkeeping.
type Queue struct {
	mu    sync.Mutex
	path  string
	tasks []*Task
	byID  map[string]*Task
}

// Open loads (or creates) the queue persisted at path. A corrupt file is
// moved aside (path.corrupt-<timestamp>) and the daemon starts fresh rather
// than refusing to boot.
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
		aside := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405")
		if moveErr := os.Rename(path, aside); moveErr != nil {
			return nil, fmt.Errorf("queue: corrupt %s and could not move it aside: %w", path, err)
		}
		slog.Warn("corrupt queue file moved aside; starting fresh", "component", "queue", "aside", aside)
		return q, nil
	}
	// Tasks caught mid-flight by a crash go back to the queue. A recurring
	// task caught in its instant of completion (crash between the completed
	// transition and the requeue) resumes its schedule instead of sitting
	// completed forever.
	for _, t := range tasks {
		switch {
		case t.State == StateRunning:
			t.State = StateQueued
		case t.State == StateCompleted && t.Every > 0:
			t.State = StateQueued
			t.Attempts = 0
			t.RunAt = time.Now().UTC().Add(t.Every)
		}
		q.tasks = append(q.tasks, t)
		q.byID[t.ID] = t
	}
	return q, nil
}

// Enqueue appends a task and persists the queue.
func (q *Queue) Enqueue(kind string, body json.RawMessage, runAt time.Time) (*Task, error) {
	task, _, err := q.EnqueueIdempotent(kind, "", body, runAt, 0)
	return task, err
}

// EnqueueIdempotent appends a task unless a queued/running task with the same
// IdempotencyKey already exists, in which case that task is returned with
// deduplicated=true (the submission is a retry, not new work). every > 0
// makes the task recur after each successful completion.
func (q *Queue) EnqueueIdempotent(kind, idempotencyKey string, body json.RawMessage, runAt time.Time, every time.Duration) (*Task, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if idempotencyKey != "" {
		for _, existing := range q.tasks {
			if existing.IdempotencyKey == idempotencyKey && (existing.State == StateQueued || existing.State == StateRunning) {
				copy := *existing
				return &copy, true, nil
			}
		}
	}
	now := time.Now().UTC()
	if runAt.IsZero() {
		runAt = now
	} else {
		runAt = runAt.UTC()
	}
	task := &Task{
		ID:             newID(),
		IdempotencyKey: idempotencyKey,
		Kind:           kind,
		Body:           body,
		RunAt:          runAt,
		Every:          every,
		State:          StateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	q.tasks = append(q.tasks, task)
	q.byID[task.ID] = task
	if err := q.saveLocked(); err != nil {
		// Roll the in-memory append back so memory and disk agree.
		delete(q.byID, task.ID)
		q.tasks = q.tasks[:len(q.tasks)-1]
		return nil, false, err
	}
	return task, false, nil
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
	prev := *t
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
		// Disk is the source of truth: undo the in-memory mutation.
		*t = prev
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

// Cancel parks a QUEUED task as failed with a "cancelled" marker. Running
// tasks cannot be cancelled from here (the executor is mid-flight; its
// result decides the terminal state).
func (q *Queue) Cancel(id string) (Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[id]
	if !ok {
		return Task{}, fmt.Errorf("queue: unknown task %s", id)
	}
	if t.State != StateQueued {
		return Task{}, fmt.Errorf("queue: task %s is %s; only queued tasks can be cancelled", id, t.State)
	}
	prev := *t
	t.State = StateFailed
	t.LastError = "cancelled"
	t.UpdatedAt = time.Now().UTC()
	if err := q.saveLocked(); err != nil {
		*t = prev
		return Task{}, err
	}
	return *t, nil
}

// Retry requeues a failed task with its attempt budget restored.
func (q *Queue) Retry(id string) (Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[id]
	if !ok {
		return Task{}, fmt.Errorf("queue: unknown task %s", id)
	}
	if t.State != StateFailed {
		return Task{}, fmt.Errorf("queue: task %s is %s; only failed tasks can be retried", id, t.State)
	}
	prev := *t
	t.State = StateQueued
	t.Attempts = 0
	t.LastError = ""
	t.RunAt = time.Now().UTC()
	t.UpdatedAt = time.Now().UTC()
	if err := q.saveLocked(); err != nil {
		*t = prev
		return Task{}, err
	}
	return *t, nil
}

// Requeue returns a COMPLETED recurring task to the queue with a fresh
// attempt budget, due at now+every. This is the recurrence step: the
// scheduler calls it after each successful run of a task with Every > 0.
func (q *Queue) Requeue(id string, every time.Duration, now time.Time) (Task, error) {
	if every <= 0 {
		return Task{}, fmt.Errorf("queue: requeue needs a positive interval, got %s", every)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.byID[id]
	if !ok {
		return Task{}, fmt.Errorf("queue: unknown task %s", id)
	}
	if t.State != StateCompleted {
		return Task{}, fmt.Errorf("queue: task %s is %s; only completed tasks can be requeued", id, t.State)
	}
	prev := *t
	t.State = StateQueued
	t.Attempts = 0
	t.LastError = ""
	t.RunAt = now.UTC().Add(every)
	t.UpdatedAt = now.UTC()
	if err := q.saveLocked(); err != nil {
		*t = prev
		return Task{}, err
	}
	return *t, nil
}

// PruneFinished drops every completed/failed task and returns how many were
// removed. Pending work is never touched -- and recurring (Every > 0) tasks
// count as pending even in their instant of completion: pruning one would
// silently cancel a schedule, so they survive and requeue instead.
func (q *Queue) PruneFinished() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := make([]*Task, 0, len(q.tasks))
	pruned := 0
	for _, t := range q.tasks {
		if (t.State == StateCompleted || t.State == StateFailed) && t.Every <= 0 {
			pruned++
			continue
		}
		kept = append(kept, t)
	}
	if pruned == 0 {
		return 0, nil
	}
	q.tasks = kept
	q.byID = make(map[string]*Task, len(kept))
	for _, t := range kept {
		q.byID[t.ID] = t
	}
	if err := q.saveLocked(); err != nil {
		return 0, err
	}
	return pruned, nil
}

// pruneLocked drops the oldest finished tasks beyond maxFinishedTasks.
// Caller holds mu. Finished tasks are kept for inspection only; pruning them
// does not lose pending work. Recurring tasks (Every > 0) are never dropped
// here even mid-completion: they are a live schedule, not history.
func (q *Queue) pruneLocked() {
	finishedSeen := 0
	kept := make([]*Task, 0, len(q.tasks))
	for i := len(q.tasks) - 1; i >= 0; i-- {
		t := q.tasks[i]
		if state := t.State; state == StateCompleted || state == StateFailed {
			if t.Every > 0 && state == StateCompleted {
				kept = append(kept, t)
				continue
			}
			finishedSeen++
			if finishedSeen > maxFinishedTasks {
				continue // an old finished task beyond the inspection cap
			}
		}
		kept = append(kept, t)
	}
	if len(kept) == len(q.tasks) {
		return
	}
	// kept was built newest-first; restore enqueue order and the index.
	q.tasks = q.tasks[:0]
	for i := len(kept) - 1; i >= 0; i-- {
		q.tasks = append(q.tasks, kept[i])
	}
	q.byID = make(map[string]*Task, len(q.tasks))
	for _, t := range q.tasks {
		q.byID[t.ID] = t
	}
}

// saveLocked writes the queue atomically (temp file + fsync + rename).
// Caller holds mu. Finished tasks are pruned here too: every persist keeps
// the file bounded, no matter which operation triggered it.
func (q *Queue) saveLocked() error {
	q.pruneLocked()
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
	// fsync BEFORE rename: a power cut must never leave a half-written
	// queue file behind under the canonical name.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("queue: fsync: %w", err)
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
