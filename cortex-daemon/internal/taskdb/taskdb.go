// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package taskdb is the daemon's real SQL surface for task scripts: a
// SQLite database (modernc.org/sqlite — pure Go, no cgo) persisted in the
// data dir. Task scripts reach it through `native.db.query` /
// `native.db.exec`; the sync script's outbox view is a separate native
// (`outbox.entries`) and does not touch this database.
//
// The database is the script's durable scratch space — it survives
// restarts, so recurring tasks can accumulate state (cursors, watermarks,
// denormalized mirrors). It is NOT the task queue (internal/queue owns its
// own JSON file) and NOT the PWA's notes.
package taskdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the sql.DB with the pragmas and value conversions every caller
// would otherwise repeat.
type DB struct {
	sql *sql.DB
}

// Open creates (or opens) the SQLite file at path and applies the
// connection pragmas. Call Verify to fail fast on an unusable file.
func Open(path string) (*DB, error) {
	sql, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("taskdb: open %s: %w", path, err)
	}
	// WAL: readers never block the writer; a crash leaves the -wal file to
	// recover from. busy_timeout: concurrent task + operator access waits
	// instead of failing with SQLITE_BUSY.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := sql.Exec(pragma); err != nil {
			sql.Close()
			return nil, fmt.Errorf("taskdb: %s: %w", pragma, err)
		}
	}
	return &DB{sql: sql}, nil
}

// Verify pings the database (used by doctor and tests).
func (d *DB) Verify(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("taskdb: not configured")
	}
	return d.sql.PingContext(ctx)
}

// Close releases the database handle.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	return d.sql.Close()
}

// Query runs a read and returns the rows as JSON-ready maps keyed by column
// name. Values convert to their JSON equivalents (TEXT→string,
// INTEGER→int64, REAL→float64, BLOB→string, NULL→nil, timestamps→RFC 3339).
func (d *DB) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if d == nil {
		return nil, fmt.Errorf("taskdb: not configured")
	}
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("taskdb: query: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("taskdb: columns: %w", err)
	}
	out := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, fmt.Errorf("taskdb: scan: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = jsonSafe(values[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("taskdb: rows: %w", err)
	}
	return out, nil
}

// Exec runs a write statement and reports the affected row count.
func (d *DB) Exec(ctx context.Context, statement string, args ...any) (int64, error) {
	if d == nil {
		return 0, fmt.Errorf("taskdb: not configured")
	}
	result, err := d.sql.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, fmt.Errorf("taskdb: exec: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("taskdb: affected: %w", err)
	}
	return affected, nil
}

// jsonSafe converts a driver value into something JSON-marshalling keeps
// intact (the stdio protocol carries every native result as JSON).
func jsonSafe(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	default:
		return value
	}
}
