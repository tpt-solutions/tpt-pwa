// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewJSONEmitsParseableRecords(t *testing.T) {
	var out bytes.Buffer
	logger, err := New(FormatJSON, "info", &out)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("listening", "component", "server", "addr", "127.0.0.1:9911")

	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out.String())
	}
	if record["msg"] != "listening" || record["component"] != "server" || record["addr"] != "127.0.0.1:9911" {
		t.Fatalf("fields missing: %v", record)
	}
	if record["level"] != "INFO" {
		t.Fatalf("level: %v", record["level"])
	}
	if _, ok := record["time"]; !ok {
		t.Fatalf("no timestamp: %v", record)
	}
}

func TestNewTextIsSingleLineReadable(t *testing.T) {
	var out bytes.Buffer
	logger, err := New(FormatText, "", &out) // empty = text/info defaults
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("task failed", "component", "scheduler", "task", "t1", "error", "boom")

	line := out.String()
	if !strings.Contains(line, "level=WARN") || !strings.Contains(line, "msg=\"task failed\"") ||
		!strings.Contains(line, "component=scheduler") || !strings.Contains(line, "task=t1") {
		t.Fatalf("unexpected text record: %s", line)
	}
	if strings.Count(strings.TrimSpace(line), "\n") != 0 {
		t.Fatalf("want one line, got: %q", line)
	}
}

func TestNewLevelFiltering(t *testing.T) {
	var out bytes.Buffer
	logger, err := New(FormatJSON, "warn", &out)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("chatter")
	logger.Info("routine")
	logger.Warn("important")

	if strings.Contains(out.String(), "chatter") || strings.Contains(out.String(), "routine") {
		t.Fatalf("records below warn leaked: %s", out.String())
	}
	if !strings.Contains(out.String(), "important") {
		t.Fatalf("warn record dropped: %s", out.String())
	}
}

func TestNewRejectsUnknownFormatAndLevel(t *testing.T) {
	if _, err := New("csv", "info", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown log format") {
		t.Fatalf("format: %v", err)
	}
	if _, err := New(FormatJSON, "verbose", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown log level") {
		t.Fatalf("level: %v", err)
	}
}

func TestConfigureSwapsTheDefault(t *testing.T) {
	var out bytes.Buffer
	// Capture the previous default so the swap doesn't leak into other tests.
	previous := slog.Default()
	defer slog.SetDefault(previous)

	logger, err := New(FormatJSON, "info", &out)
	if err != nil {
		t.Fatal(err)
	}
	slog.SetDefault(logger)
	slog.Error("from the default", "component", "queue")
	if !strings.Contains(out.String(), `"from the default"`) {
		t.Fatalf("default logger not routed: %s", out.String())
	}
}
