// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package logging configures the daemon's one logger. Human operators get
// plain text by default; tooling (systemd, Docker, gomobile embedders) can
// flip to one-JSON-object-per-line via -log-format json and scrape levels
// and component attrs without regexes. Every library call site logs through
// slog's default logger, so Configure is the single setup point.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Supported -log-format values.
const (
	FormatText = "text"
	FormatJSON = "json"
)

var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// Validate reports whether the format/level pair is recognized, with an
// error naming the valid values (surfaced verbatim by -config and flags).
func Validate(format, level string) error {
	switch format {
	case "", FormatText, FormatJSON:
	default:
		return fmt.Errorf("unknown log format %q (want %q or %q)", format, FormatText, FormatJSON)
	}
	if level != "" {
		if _, ok := levels[strings.ToLower(level)]; !ok {
			return fmt.Errorf("unknown log level %q (want debug, info, warn, or error)", level)
		}
	}
	return nil
}

// New builds a logger writing to out. Empty format/level mean text/info.
func New(format, level string, out io.Writer) (*slog.Logger, error) {
	if err := Validate(format, level); err != nil {
		return nil, err
	}
	if format == "" {
		format = FormatText
	}
	slogLevel := slog.LevelInfo
	if level != "" {
		slogLevel = levels[strings.ToLower(level)]
	}

	opts := &slog.HandlerOptions{Level: slogLevel}
	var handler slog.Handler
	if format == FormatJSON {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}
	return slog.New(handler), nil
}

// Configure installs the process-wide default logger (stderr). Library call
// sites resolve slog.Default() per call, so anything logged after this —
// including from tests or embedders that never went near the CLI — is
// formatted consistently.
func Configure(format, level string) error {
	logger, err := New(format, level, os.Stderr)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	return nil
}
