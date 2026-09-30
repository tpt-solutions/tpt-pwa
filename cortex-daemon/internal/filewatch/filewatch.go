// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package filewatch is the event side of watch tasks: it keeps fsnotify
// watches aligned with the queue's active watch patterns and wakes the
// matching tasks when a file changes.
//
// Patterns are data-dir-relative globs ("inbox/*.csv"); only the pattern's
// directory is watched (fsnotify is non-recursive), and only names directly
// in that directory match the glob's last element. Events are debounced per
// pattern -- editors fire several events per save, and a wake just makes the
// task due, so coalescing is about not double-triggering a run that is
// still queued.
//
// Watch paths never leave the data-dir sandbox: every pattern is
// re-contained against the data dir on every resync, so a hand-edited queue
// file cannot smuggle in a watch over arbitrary paths.
package filewatch

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
)

// debounce pauses between an fs event burst and the wake it produces.
const debounce = 500 * time.Millisecond

// watch is one resolved pattern: the directory on disk plus the glob its
// events are matched against (the pattern's last element, since the watched
// directory covers everything before it).
type watch struct {
	pattern string // the task's data-dir-relative glob, verbatim
	dir     string // absolute directory under fsnotify
	glob    string // matched against names directly inside dir
}

// Loop runs until ctx is cancelled. Every poll interval it resyncs the
// watched-directory set with the queue's active watch tasks (one queue
// snapshot), then serves events until the next tick. Errors are logged,
// never fatal -- a broken watcher degrades to time-based tasks only, and
// the next resync retries.
func Loop(ctx context.Context, tasks *queue.Queue, dataDir string, pollEvery time.Duration, log *slog.Logger) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn("file watcher unavailable; watch tasks stay asleep", "component", "filewatch", "error", err)
		return
	}
	defer watcher.Close()

	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		log.Warn("file watcher disabled: data dir unresolved", "component", "filewatch", "error", err)
		return
	}

	var mu sync.Mutex
	var active []watch
	timers := make(map[string]*time.Timer) // pattern -> debounce timer
	watched := make(map[string]bool)       // absolute dir -> under fsnotify

	resync := func() {
		mu.Lock()
		defer mu.Unlock()

		seen := make(map[string]bool)
		var resolved []watch
		for _, task := range tasks.List() {
			if task.State != queue.StateQueued || task.Watch == "" {
				continue
			}
			seen[task.Watch] = true
			if w, ok := resolve(absDataDir, task.Watch); ok {
				resolved = append(resolved, w)
			} else {
				log.Warn("watch pattern escapes the data dir; task stays asleep", "component", "filewatch", "task", task.ID, "pattern", task.Watch)
			}
		}
		active = resolved

		for pattern, timer := range timers {
			if !seen[pattern] {
				timer.Stop()
				delete(timers, pattern)
			}
		}

		needed := make(map[string]bool, len(active))
		for _, w := range active {
			needed[w.dir] = true
		}
		for dir := range watched {
			if !needed[dir] {
				_ = watcher.Remove(dir)
				delete(watched, dir)
			}
		}
		for _, w := range active {
			if watched[w.dir] {
				continue
			}
			if err := watcher.Add(w.dir); err != nil {
				// Typically "directory does not exist yet": resync retries.
				log.Debug("watch directory unavailable; retrying on resync", "component", "filewatch", "dir", w.dir)
				continue
			}
			watched[w.dir] = true
		}
	}

	// wakeSoon debounces: one timer per pattern, reset on every event burst.
	wakeSoon := func(pattern string) {
		mu.Lock()
		defer mu.Unlock()
		if timer := timers[pattern]; timer != nil {
			timer.Reset(debounce)
			return
		}
		timers[pattern] = time.AfterFunc(debounce, func() { fire(tasks, &mu, timers, pattern) })
	}

	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	resync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resync()
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			mu.Lock()
			var matched []string
			for _, w := range active {
				if matchEvent(w, event.Name) {
					matched = append(matched, w.pattern)
				}
			}
			mu.Unlock()
			for _, pattern := range matched {
				wakeSoon(pattern)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Warn("file watcher error", "component", "filewatch", "error", err)
		}
	}
}

// fire wakes every queued task carrying the pattern. The mutex guards the
// shared timers/pattern state; the queue locks itself.
func fire(tasks *queue.Queue, mu *sync.Mutex, timers map[string]*time.Timer, pattern string) {
	mu.Lock()
	delete(timers, pattern)
	mu.Unlock()

	var ids []string
	for _, task := range tasks.List() {
		if task.State == queue.StateQueued && task.Watch == pattern {
			ids = append(ids, task.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if _, err := tasks.WakeTasks(ids, time.Now()); err != nil {
		slog.Warn("waking watch task failed", "component", "filewatch", "pattern", pattern, "error", err)
	}
}

// resolve contains a pattern inside the data dir and splits it into the
// directory to watch plus the glob to match against.
func resolve(absDataDir, pattern string) (watch, bool) {
	cleaned := filepath.Clean(filepath.Join(absDataDir, filepath.FromSlash(pattern)))
	if !within(absDataDir, cleaned) {
		return watch{}, false
	}
	dir := filepath.Dir(cleaned)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if !within(absDataDir, dir) {
		return watch{}, false
	}
	return watch{pattern: pattern, dir: dir, glob: filepath.Base(cleaned)}, true
}

// matchEvent reports whether an fsnotify path is a name directly inside the
// watched directory that the pattern's glob matches. The directory itself,
// escapes, and deeper paths never match (fsnotify is non-recursive -- the
// watched dir IS the pattern's dir, so any multi-element remainder is out).
func matchEvent(w watch, path string) bool {
	rel, err := filepath.Rel(w.dir, path)
	if err != nil || rel == "." {
		return false
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	if strings.Contains(rel, string(filepath.Separator)) || strings.Contains(rel, "/") {
		return false
	}
	ok, err := filepath.Match(w.glob, rel)
	return err == nil && ok
}

// within reports whether path is root itself or inside it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
