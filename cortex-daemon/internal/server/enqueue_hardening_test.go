// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func rpcCall(t *testing.T, method string, params any) (any, error) {
	t.Helper()
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return handlers(t.TempDir(), mustQueue(t), nil)[method](context.Background(), encoded)
}

// Unknown kinds are rejected at enqueue time, not after retries.
func TestEnqueueRejectsUnknownKind(t *testing.T) {
	_, err := rpcCall(t, "cortex.task.enqueue", map[string]any{"kind": "rm_rf", "payload": map[string]string{}})
	if err == nil {
		t.Fatal("unknown task kind must be rejected")
	}
	if _, ok := taskKinds["syncNotes"]; !ok {
		t.Fatal("syncNotes must stay a known kind")
	}
}

// A submission carrying both (or neither) of entries/payload is ambiguous.
func TestEnqueueRequiresExactlyOneOfEntriesOrPayload(t *testing.T) {
	if _, err := rpcCall(t, "cortex.task.enqueue", map[string]any{
		"kind":    "syncNotes",
		"entries": []any{map[string]string{"id": "a"}},
		"payload": map[string]string{"x": "y"},
	}); err == nil {
		t.Fatal("entries AND payload must be rejected")
	}
	if _, err := rpcCall(t, "cortex.task.enqueue", map[string]any{"kind": "syncNotes"}); err == nil {
		t.Fatal("neither entries nor payload must be rejected")
	}
}

// Retried hand-offs with the same batchId deduplicate instead of queuing the
// batch twice.
func TestEnqueueIdempotencyKeyDeduplicatesBatches(t *testing.T) {
	taskQueue := mustQueue(t)
	encoded, _ := json.Marshal(map[string]any{
		"kind":    "syncNotes",
		"batchId": "sync-abc123",
		"entries": []any{map[string]string{"id": "n1:create"}},
	})
	first, err := taskEnqueueHandler(taskQueue, nil)(context.Background(), encoded)
	if err != nil {
		t.Fatalf("first submission: %v", err)
	}
	second, err := taskEnqueueHandler(taskQueue, nil)(context.Background(), encoded)
	if err != nil {
		t.Fatalf("retry submission: %v", err)
	}
	if first.(map[string]any)["taskId"] != second.(map[string]any)["taskId"] {
		t.Fatalf("same batchId must return the same task: %v vs %v", first, second)
	}
	if second.(map[string]any)["deduplicated"] != true {
		t.Fatal("retry must be flagged as deduplicated")
	}
	if got := len(taskQueue.List()); got != 1 {
		t.Fatalf("queue holds %d tasks, want 1", got)
	}
	// The PWA transport reads `accepted` to verify the whole batch landed.
	if first.(map[string]any)["accepted"] != 1 {
		t.Fatalf("accepted count wrong: %v", first)
	}
}

// The fs.write sandbox resolves symlinks: a link inside dataDir pointing
// outside must not become an escape hatch, and writes land atomically.
func TestFSWriteResolvesSymlinksAndWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	write := handlers(dir, mustQueue(t), nil)["fs.write"]

	// Escape via a symlinked subdirectory.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unavailable on this platform/filesystem: %v", err)
	}
	params, _ := json.Marshal(map[string]string{
		"path":   "escape/evil.txt",
		"buffer": base64.StdEncoding.EncodeToString([]byte("nope")),
	})
	if _, err := write(context.Background(), params); err == nil {
		t.Fatal("symlink escape must be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.txt")); err == nil {
		t.Fatal("file landed outside the sandbox")
	}

	// A normal write still works and lands under the real root.
	ok, _ := json.Marshal(map[string]string{
		"path":   "nested/ok.txt",
		"buffer": base64.StdEncoding.EncodeToString([]byte("hello")),
	})
	if _, err := write(context.Background(), ok); err != nil {
		t.Fatalf("in-sandbox write rejected: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested", "ok.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("write did not land: %v %q", err, data)
	}

	// Overwrite goes through temp+rename: no *.tmp-* litter may remain.
	entries, _ := os.ReadDir(filepath.Join(dir, "nested"))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp-*" || len(entry.Name()) > 4 && entry.Name()[len(entry.Name())-6:] == ".tmp-*" {
			t.Fatalf("temp file litter left behind: %s", entry.Name())
		}
	}
}
