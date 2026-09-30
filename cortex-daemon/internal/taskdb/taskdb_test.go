// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package taskdb

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "cortex.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestExecAndQueryRoundTrip(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	affected, err := db.Exec(ctx, "CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT, score REAL, payload BLOB)")
	if err != nil || affected != 0 {
		t.Fatalf("create: %d %v", affected, err)
	}
	// TaskDB is the script's durable scratch space: writes persist across
	// "restarts" (reopen below proves it).
	inserted, err := db.Exec(ctx, "INSERT INTO notes (title, score, payload) VALUES (?, ?, ?)", "hello", 9.5, []byte{0xde, 0xad})
	if err != nil || inserted != 1 {
		t.Fatalf("insert: %d %v", inserted, err)
	}

	rows, err := db.Query(ctx, "SELECT id, title, score, payload FROM notes WHERE title = ?", "hello")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row["title"] != "hello" {
		t.Fatalf("text value: %v", row["title"])
	}
	if row["id"] != int64(1) {
		t.Fatalf("integer value: %v", row["id"])
	}
	if row["score"] != 9.5 {
		t.Fatalf("real value: %v", row["score"])
	}
	// BLOBs become strings so the JSON protocol carries them.
	if row["payload"] != string([]byte{0xde, 0xad}) {
		t.Fatalf("blob value: %v", row["payload"])
	}
}

func TestQueryTypeCoverage(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "CREATE TABLE t (b BOOLEAN, n NUMERIC, z TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t VALUES (?, ?, ?)", true, nil, "x"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := db.Query(ctx, "SELECT b, n, z FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	row := rows[0]
	// SQLite stores BOOLEAN as INTEGER: scripts see 1/0, the JSON protocol
	// carries the number.
	if row["b"] != int64(1) || row["n"] != nil || row["z"] != "x" {
		t.Fatalf("values: %#v", row)
	}
}

func TestQueryReturnsEveryRowInOrder(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "CREATE TABLE t (i INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := db.Exec(ctx, "INSERT INTO t VALUES (?)", i); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	rows, err := db.Query(ctx, "SELECT i FROM t ORDER BY i")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows", len(rows))
	}
	for i, row := range rows {
		if row["i"] != int64(i) {
			t.Fatalf("row %d: %v", i, row["i"])
		}
	}
}

func TestErrorsCarryTheStatementProblem(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	if _, err := db.Query(ctx, "SELECT * FROM missing_table"); err == nil {
		t.Fatal("querying a missing table must fail")
	} else if !strings.Contains(err.Error(), "taskdb") {
		t.Fatalf("error should be namespaced: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO missing_table VALUES (1)"); err == nil {
		t.Fatal("exec against a missing table must fail")
	}
}

func TestNilDBFailsWithAClearMessage(t *testing.T) {
	var db *DB
	ctx := context.Background()
	if _, err := db.Query(ctx, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("query on nil db: %v", err)
	}
	if _, err := db.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("exec on nil db must fail")
	}
	if err := db.Verify(ctx); !errors.Is(err, err) || err == nil {
		t.Fatalf("verify on nil db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close on nil db: %v", err)
	}
}

func TestStatePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cortex.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db1.Exec(context.Background(), "CREATE TABLE state (k TEXT, v TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db1.Exec(context.Background(), "INSERT INTO state VALUES ('cursor', '42')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db1.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	rows, err := db2.Query(context.Background(), "SELECT v FROM state WHERE k = 'cursor'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || rows[0]["v"] != "42" {
		t.Fatalf("state lost across reopen: %v", rows)
	}
}
