// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package scheduler drains the persistent queue: whenever the OS reports
// connectivity, due tasks are handed to an Executor and retried with backoff
// until they succeed or exhaust their attempts. This is the server side of
// spec §4 Path A -- the PWA may be closed; the daemon keeps working.
package scheduler

import (
	"context"
	"log"
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
		err := s.Executor.Execute(ctx, task)
		switch {
		case err == nil:
			s.transition(task.ID, queue.StateCompleted, "", s.MaxAttempts)
		default:
			log.Printf("scheduler: task %s (%s) attempt %d failed: %v", task.ID, task.Kind, task.Attempts+1, err)
			s.transition(task.ID, queue.StateQueued, err.Error(), s.MaxAttempts)
		}
	}
}

func (s *Scheduler) transition(taskID string, state queue.State, errMsg string, maxAttempts int) {
	if _, err := s.Queue.Transition(taskID, state, errMsg, maxAttempts, time.Now()); err != nil {
		log.Printf("scheduler: transition task %s to %s: %v", taskID, state, err)
	}
	if s.OnTransition != nil {
		s.OnTransition(taskID, state)
	}
}
