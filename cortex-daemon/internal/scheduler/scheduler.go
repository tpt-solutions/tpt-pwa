// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package scheduler drains the persistent queue: whenever the OS reports
// connectivity, due tasks are handed to an Executor and retried with backoff
// until they succeed or exhaust their attempts. This is the server side of
// spec §4 Path A -- the PWA may be closed; the daemon keeps working.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

// Executor runs one task. Returning an error requeues it (bounded retries).
type Executor interface {
	Execute(ctx context.Context, task queue.Task) error
}

// Connectivity reports whether the machine currently has network access.
type Connectivity func(ctx context.Context) bool

// Scheduler polls the queue and executes due tasks.
type Scheduler struct {
	Queue       *queue.Queue
	Executor    Executor
	Connected   Connectivity
	PollEvery   time.Duration
	MaxAttempts int
	// TaskTimeout bounds one Execute call so a hung executor cannot stall the
	// serial queue (default 2m).
	TaskTimeout time.Duration
	// OnTransition, when set, is notified after every state change (used to
	// push cortex.event.taskCompleted notifications to connected PWAs).
	OnTransition func(taskID string, state queue.State)
}

// Run blocks until ctx is cancelled, executing due tasks each tick.
func (s *Scheduler) Run(ctx context.Context) {
	if s.PollEvery <= 0 {
		s.PollEvery = 5 * time.Second
	}
	if s.MaxAttempts <= 0 {
		s.MaxAttempts = 8
	}
	if s.TaskTimeout <= 0 {
		s.TaskTimeout = 2 * time.Minute
	}
	ticker := time.NewTicker(s.PollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// Tick runs one scheduling pass; exported for tests.
func (s *Scheduler) Tick(ctx context.Context) { s.tick(ctx) }

func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now()
	for _, task := range s.Queue.Due(now) {
		if s.Connected != nil && !s.Connected(ctx) {
			return // offline: everything stays queued for a later tick
		}
		if _, err := s.Queue.Transition(task.ID, queue.StateRunning, "", s.MaxAttempts, now); err != nil {
			continue
		}
		// One task can never hold the serial queue hostage: each execution
		// runs under its own deadline (a timeout is treated as transient).
		execCtx, cancel := context.WithTimeout(ctx, s.TaskTimeout)
		err := s.Executor.Execute(execCtx, task)
		cancel()
		var permanent *queue.PermanentError
		switch {
		case err == nil:
			s.transition(task.ID, queue.StateCompleted, "", s.MaxAttempts)
		case errors.As(err, &permanent):
			// Retrying can never fix it (e.g. the endpoint rejected an entry
			// with 4xx): park it for inspection right away.
			slog.Error("task failed permanently", "component", "scheduler", "task", task.ID, "kind", task.Kind, "error", err)
			s.transition(task.ID, queue.StateFailed, err.Error(), s.MaxAttempts)
		default:
			slog.Warn("task attempt failed", "component", "scheduler", "task", task.ID, "kind", task.Kind, "attempt", task.Attempts+1, "error", err)
			s.transition(task.ID, queue.StateQueued, err.Error(), s.MaxAttempts)
		}
	}
}

func (s *Scheduler) transition(taskID string, state queue.State, errMsg string, maxAttempts int) {
	// A requeue past max attempts is parked as failed by the queue, so report
	// the state the task actually landed in, not the one requested.
	task, err := s.Queue.Transition(taskID, state, errMsg, maxAttempts, time.Now())
	if err != nil {
		slog.Warn("task transition failed", "component", "scheduler", "task", taskID, "state", string(state), "error", err)
	} else {
		state = task.State
	}
	if s.OnTransition != nil {
		s.OnTransition(taskID, state)
	}
}
