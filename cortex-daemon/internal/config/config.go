// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package config loads the daemon's optional JSON config file
// (`-config path`). Keys mirror the CLI flags, so one mental model covers
// both; explicit flags win over file values, and file values win over
// built-in defaults. Parsing is strict on purpose — a typo like "auth_token"
// must fail loudly instead of silently running with loopback trust.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/tpt-solutions/tpt-pwa/cortex-daemon/internal/logging"
)

// File is the on-disk shape. Strings stay strings (so "" means unset and
// merging with flag defaults is trivial); durations are flag-syntax strings
// ("5s", "1m30s"); max-attempts is nilable to distinguish "unset" from 0.
type File struct {
	Addr         string `json:"addr"`
	SyncEndpoint string `json:"sync-endpoint"`
	Poll         string `json:"poll"`
	MaxAttempts  *int   `json:"max-attempts"`
	QueuePath    string `json:"queue"`
	DataDir      string `json:"data-dir"`
	EnginePath   string `json:"engine"`
	EngineScript string `json:"engine-script"`
	AuthToken    string `json:"auth-token"`
	LogFormat    string `json:"log-format"`
	LogLevel     string `json:"log-level"`
	AllowNatives string `json:"allow-natives"`
}

// Load reads and validates a config file. Values are checked here — not
// at flag-default time — so `-h` never dies on a half-written file and the
// error always names the offending path.
func Load(path string) (*File, error) {
	if path == "" {
		return &File{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file File
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	// Reject trailing content ("{}garbage" decodes fine as {} otherwise).
	if dec.More() {
		return nil, fmt.Errorf("config %s: unexpected content after the JSON object", path)
	}

	if file.Poll != "" {
		if _, err := time.ParseDuration(file.Poll); err != nil {
			return nil, fmt.Errorf("config %s: invalid poll duration %q (want flag syntax like \"5s\" or \"1m30s\")", path, file.Poll)
		}
	}
	if file.MaxAttempts != nil && *file.MaxAttempts < 1 {
		return nil, fmt.Errorf("config %s: max-attempts must be >= 1 (got %d)", path, *file.MaxAttempts)
	}
	if err := logging.Validate(file.LogFormat, file.LogLevel); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &file, nil
}
