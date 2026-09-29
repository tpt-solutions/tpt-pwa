// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package doctor

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthyEnvironmentHasNoFailures(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	healthy := Run(context.Background(), Options{
		Addr:      "127.0.0.1:0", // port 0: nothing can be "in use"
		QueuePath: filepath.Join(dir, "queue.json"),
		DataDir:   filepath.Join(dir, "data"),
	}, &out)
	if !healthy {
		t.Fatalf("fresh environment must be healthy:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[OK ] version:") {
		t.Fatalf("version check missing:\n%s", out.String())
	}
}

func TestNonLoopbackAddressFails(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	healthy := Run(context.Background(), Options{
		Addr:      "0.0.0.0:9911",
		QueuePath: filepath.Join(dir, "queue.json"),
		DataDir:   filepath.Join(dir, "data"),
	}, &out)
	if healthy {
		t.Fatalf("non-loopback binding must fail the check:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[FAIL] origin:") {
		t.Fatalf("origin check must fail:\n%s", out.String())
	}
}

func TestJSONRendering(t *testing.T) {
	checks := All(context.Background(), Options{Addr: "127.0.0.1:0", QueuePath: filepath.Join(t.TempDir(), "q.json"), DataDir: t.TempDir()})
	encoded, err := JSON(checks)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"Verdict"`)) || !bytes.Contains(encoded, []byte(`"Name"`)) {
		t.Fatalf("unexpected JSON shape: %s", encoded)
	}
}
