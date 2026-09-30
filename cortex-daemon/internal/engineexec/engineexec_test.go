// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package engineexec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/queue"
	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/taskdb"
)

// TestMain doubles as the fake cortex-engine binary: when re-executed with
// ENGINEEXEC_FAKE set, it speaks the host protocol instead of running tests.
func TestMain(m *testing.M) {
	switch os.Getenv("ENGINEEXEC_FAKE") {
	case "":
		os.Exit(m.Run())
	case "exec-host":
		if len(os.Args) > 1 && os.Args[1] == "manifest" {
			// The engine's `manifest` subcommand: emit this fake script's
			// native-call surface (ENGINEEXEC_FAKE_NATIVES, JSON array).
			natives := os.Getenv("ENGINEEXEC_FAKE_NATIVES")
			if natives == "" {
				natives = `["db.query", "http.post", "net.isConnected"]`
			}
			fmt.Println(natives)
			os.Exit(0)
		}
		runFakeEngine()
		os.Exit(0)
	case "probe":
		// Mimic the real CLI's exit code for a missing --script file.
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func runFakeEngine() {
	reader := bufio.NewReader(os.Stdin)
	responses := make(map[uint64]map[string]any)

	call := func(id uint64, method string, params ...any) map[string]any {
		request, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, string(request))
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			os.Exit(1)
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			os.Exit(1)
		}
		responses[id] = response
		return response
	}

	rows := call(1, "outbox.entries")
	connected := call(2, "net.isConnected")
	if connected["error"] != nil {
		os.Exit(1)
	}
	// Pull the injected endpoint out of row 1 and post it, like sync.ctx does.
	endpoint := "https://unused.example/sync"
	if result, ok := rows["result"].([]any); ok && len(result) > 0 {
		if row, ok := result[0].(map[string]any); ok {
			if ep, ok := row["endpoint"].(string); ok {
				endpoint = ep
			}
			call(3, "http.post", endpoint, row)
		}
	}

	logPath := os.Getenv("ENGINEEXEC_FAKE_LOG")
	if logPath != "" {
		summary, _ := json.Marshal(map[string]any{
			"rows":      rows["result"],
			"connected": connected["result"],
			"post":      responses[3],
		})
		os.WriteFile(logPath, summary, 0o644)
	}
}

func TestExecuteDrivesScriptThroughHostProtocol(t *testing.T) {
	t.Setenv("ENGINEEXEC_FAKE", "exec-host")

	var received [][]byte
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = append(received, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	logPath := filepath.Join(t.TempDir(), "fake-engine.log")
	t.Setenv("ENGINEEXEC_FAKE_LOG", logPath)

	body, err := json.Marshal(map[string]any{
		"entries": []map[string]any{
			{"id": "n1:create", "kind": "note", "action": "create", "payload": map[string]any{"id": "n1", "title": "hello"}, "queuedAt": 42},
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	executor := &Executor{
		EnginePath: os.Args[0], // the test binary, re-executed as the fake engine
		Endpoint:   endpoint.URL,
		Connected:  func(context.Context) bool { return true },
	}
	if err := executor.Execute(context.Background(), queue.Task{ID: "t1", Kind: "syncNotes", Body: body}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	summary, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fake engine log missing: %v", err)
	}
	var parsed struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(summary, &parsed); err != nil {
		t.Fatalf("decode log %s: %v", summary, err)
	}
	if len(parsed.Rows) != 1 || parsed.Rows[0]["id"] != "n1:create" {
		t.Fatalf("script did not receive the task rows: %v", parsed.Rows)
	}
	if parsed.Rows[0]["endpoint"] != endpoint.URL {
		t.Fatalf("rows must carry the configured endpoint, got %v", parsed.Rows[0]["endpoint"])
	}
	if len(received) != 1 || !strings.Contains(string(received[0]), `"n1"`) {
		t.Fatalf("http.post must reach the endpoint with the row payload, got %s", received)
	}
}

// The host side of db.*: outbox.entries serves the task's rows, and
// db.exec/db.query reach the configured SQLite database (the real engine
// e2e below proves the same through an actual Rust VM).
func TestExecuteServesOutboxEntriesAndRealSQL(t *testing.T) {
	db, err := taskdb.Open(filepath.Join(t.TempDir(), "cortex.db"))
	if err != nil {
		t.Fatalf("open taskdb: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(ctx, "CREATE TABLE markers (id TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	executor := &Executor{
		EnginePath: os.Args[0],
		DB:         db,
	}
	task := queue.Task{
		ID:   "t1",
		Kind: "syncNotes",
		Body: json.RawMessage(`{"entries":[{"id":"n1:create","endpoint":"https://x"}]}`),
	}
	requests := []protocolRequest{
		{ID: 1, Method: "outbox.entries"},
		{ID: 2, Method: "db.exec", Params: []json.RawMessage{
			json.RawMessage(`"INSERT INTO markers (id) VALUES (?)"`),
			json.RawMessage(`"n9"`),
		}},
		{ID: 3, Method: "db.query", Params: []json.RawMessage{
			json.RawMessage(`"SELECT id FROM markers"`),
		}},
	}

	for _, request := range requests {
		response := executor.respond(ctx, task, request)
		if response.Error != "" {
			t.Fatalf("%s failed: %s", request.Method, response.Error)
		}
		switch request.Method {
		case "outbox.entries":
			var rows []map[string]any
			if err := json.Unmarshal(response.Result, &rows); err != nil || len(rows) != 1 {
				t.Fatalf("outbox.entries: %s (%v)", response.Result, err)
			}
			if rows[0]["id"] != "n1:create" {
				t.Fatalf("outbox row: %v", rows[0])
			}
		case "db.exec":
			var affected float64
			if err := json.Unmarshal(response.Result, &affected); err != nil || affected != 1 {
				t.Fatalf("db.exec affected: %s (%v)", response.Result, err)
			}
		case "db.query":
			var rows []map[string]any
			if err := json.Unmarshal(response.Result, &rows); err != nil || len(rows) != 1 || rows[0]["id"] != "n9" {
				t.Fatalf("db.query rows: %s (%v)", response.Result, err)
			}
		}
	}
}

func TestExecuteEnforcesTheNativeAllowlist(t *testing.T) {
	var posts int
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	body, err := json.Marshal(map[string]any{
		"entries": []map[string]any{
			{"id": "n1:create", "kind": "note", "action": "create", "payload": map[string]any{"id": "n1"}, "queuedAt": 42},
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	newExecutor := func() *Executor {
		return &Executor{
			EnginePath: os.Args[0], // the test binary, re-executed as the fake engine
			Endpoint:   endpoint.URL,
			Connected:  func(context.Context) bool { return true },
		}
	}

	t.Run("a script exceeding the allowlist is refused before any native fires", func(t *testing.T) {
		t.Setenv("ENGINEEXEC_FAKE", "exec-host")
		t.Setenv("ENGINEEXEC_FAKE_NATIVES", `["db.query", "http.post", "net.isConnected"]`)
		executor := newExecutor()
		executor.AllowedNatives = map[string]bool{"db.query": true, "net.isConnected": true}

		err := executor.Execute(context.Background(), queue.Task{ID: "t1", Kind: "syncNotes", Body: body})
		var permanent *queue.PermanentError
		if !errors.As(err, &permanent) {
			t.Fatalf("want a permanent error (the same bytes can never pass), got %v", err)
		}
		if !strings.Contains(err.Error(), "http.post") {
			t.Fatalf("the refusal must name the offending native: %v", err)
		}
		if posts != 0 {
			t.Fatalf("no native call may fire from a refused script, got %d posts", posts)
		}
	})

	t.Run("a script within the allowlist runs", func(t *testing.T) {
		t.Setenv("ENGINEEXEC_FAKE", "exec-host")
		t.Setenv("ENGINEEXEC_FAKE_NATIVES", `["db.query", "net.isConnected"]`)
		executor := newExecutor()
		executor.AllowedNatives = map[string]bool{"db.query": true, "net.isConnected": true}

		if err := executor.Execute(context.Background(), queue.Task{ID: "t2", Kind: "syncNotes", Body: body}); err != nil {
			t.Fatalf("execute within allowlist: %v", err)
		}
	})

	t.Run("an unrestricted executor never checks", func(t *testing.T) {
		t.Setenv("ENGINEEXEC_FAKE", "exec-host")
		t.Setenv("ENGINEEXEC_FAKE_NATIVES", `["db.query", "http.post", "net.isConnected", "fs.write"]`)
		executor := newExecutor()

		if err := executor.Execute(context.Background(), queue.Task{ID: "t3", Kind: "syncNotes", Body: body}); err != nil {
			t.Fatalf("execute unrestricted: %v", err)
		}
	})
}

func TestProbeAcceptsEngineCLI(t *testing.T) {
	t.Setenv("ENGINEEXEC_FAKE", "probe")
	executor := &Executor{EnginePath: os.Args[0]}
	if err := executor.Probe(context.Background()); err != nil {
		t.Fatalf("probe should accept exit-code-2 CLI, got %v", err)
	}
}

func TestProbeRejectsMissingBinary(t *testing.T) {
	executor := &Executor{EnginePath: "cortex-engine-not-installed-anywhere"}
	if err := executor.Probe(context.Background()); err == nil {
		t.Fatal("probe must fail for a missing engine binary")
	}
}

// TestExecuteAgainstRealEngine proves the protocol against the actual Rust
// binary. Skipped unless CORTEX_ENGINE_BIN points at a built cortex-engine
// (cargo build in cortex-engine/). CI wires this up when both toolchains are
// present.
func TestExecuteAgainstRealEngine(t *testing.T) {
	engineBin := os.Getenv("CORTEX_ENGINE_BIN")
	if engineBin == "" {
		t.Skip("CORTEX_ENGINE_BIN not set; protocol covered by the fake-engine test")
	}

	var posts []map[string]any
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		posts = append(posts, parsed)
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	body, err := json.Marshal(map[string]any{
		"entries": []map[string]any{
			{"id": "n1:create", "kind": "note", "action": "create", "payload": map[string]any{"id": "n1"}, "queuedAt": 1},
			{"id": "n2:create", "kind": "note", "action": "create", "payload": map[string]any{"id": "n2"}, "queuedAt": 2},
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	executor := &Executor{
		EnginePath: engineBin,
		Endpoint:   endpoint.URL,
		Connected:  func(context.Context) bool { return true },
	}
	if err := executor.Execute(context.Background(), queue.Task{ID: "t1", Kind: "syncNotes", Body: body}); err != nil {
		t.Fatalf("real engine execute: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("real engine must post each row, got %d posts: %v", len(posts), posts)
	}
	if posts[0]["action"] != "create" {
		t.Fatalf("unexpected posted row: %v", posts[0])
	}
}

// Proves the real engine drives db.* against a real SQLite file end to end:
// the script writes and reads through the stdio protocol, the database
// persists the effect.
func TestExecuteAgainstRealEngineRunsRealSQL(t *testing.T) {
	engineBin := os.Getenv("CORTEX_ENGINE_BIN")
	if engineBin == "" {
		t.Skip("CORTEX_ENGINE_BIN not set; protocol covered by the fake-engine test")
	}

	db, err := taskdb.Open(filepath.Join(t.TempDir(), "cortex.db"))
	if err != nil {
		t.Fatalf("open taskdb: %v", err)
	}
	defer db.Close()

	const script = `
task sqlRoundTrip() -> void {
    native.db.exec("CREATE TABLE IF NOT EXISTS visits (page TEXT, at INTEGER)");
    native.db.exec("INSERT INTO visits VALUES (?, ?)", "home", 42);
    let rows = native.db.query("SELECT page, at FROM visits");
    return rows[0].page;
}
`
	scriptPath := filepath.Join(t.TempDir(), "sql-round-trip.ctx")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}

	executor := &Executor{
		EnginePath: engineBin,
		ScriptPath: scriptPath,
		DB:         db,
	}
	if err := executor.Execute(context.Background(), queue.Task{ID: "t-sql", Kind: "syncNotes"}); err != nil {
		t.Fatalf("real engine sql execute: %v", err)
	}

	rows, err := db.Query(context.Background(), "SELECT page, at FROM visits")
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	if len(rows) != 1 || rows[0]["page"] != "home" {
		t.Fatalf("the script's insert must persist: %v", rows)
	}
}
