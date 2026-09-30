// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cortex.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadParsesEveryKey(t *testing.T) {
	path := write(t, `{
		"addr": "127.0.0.1:1234",
		"sync-endpoint": "https://example.test/sync",
		"poll": "30s",
		"max-attempts": 3,
		"queue": "q.json",
		"data-dir": "d",
		"engine": "engine.exe",
		"engine-script": "sync.ctx",
		"auth-token": "secret",
		"log-format": "json",
		"log-level": "warn"
	}`)

	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Addr != "127.0.0.1:1234" || file.SyncEndpoint != "https://example.test/sync" || file.Poll != "30s" {
		t.Fatalf("string keys mangled: %+v", file)
	}
	if file.MaxAttempts == nil || *file.MaxAttempts != 3 {
		t.Fatalf("max-attempts: %+v", file.MaxAttempts)
	}
	if file.EnginePath != "engine.exe" || file.EngineScript != "sync.ctx" || file.AuthToken != "secret" {
		t.Fatalf("engine/auth keys mangled: %+v", file)
	}
	if file.LogFormat != "json" || file.LogLevel != "warn" {
		t.Fatalf("log keys mangled: %+v", file)
	}
}

func TestLoadEmptyPathIsTheZeroConfig(t *testing.T) {
	file, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if *file != (File{}) {
		t.Fatalf("want zero config, got %+v", file)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"unknown key (typo protection)", `{"auth_token": "x"}`, "unknown field"},
		{"invalid json", `{`, "config"},
		{"trailing content", `{} garbage`, "unexpected content"},
		{"bad poll duration", `{"poll": "5 seconds"}`, "invalid poll duration"},
		{"zero max-attempts", `{"max-attempts": 0}`, "max-attempts"},
		{"negative max-attempts", `{"max-attempts": -2}`, "max-attempts"},
		{"bad log format", `{"log-format": "csv"}`, "unknown log format"},
		{"bad log level", `{"log-level": "verbose"}`, "unknown log level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.content))
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMissingFileNamesThePath(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want not-exist error, got %v", err)
	}
}
