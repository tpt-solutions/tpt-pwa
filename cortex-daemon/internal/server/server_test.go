// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package server

import (
	"path/filepath"
	"testing"
)

func TestWithinSandbox(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "data", "sandbox")
	cases := []struct {
		path  string
		valid bool
	}{
		{filepath.Join(root, "notes.txt"), true},
		{filepath.Join(root, "sub", "dir", "file.bin"), true},
		{root, true},
		{filepath.Join(string(filepath.Separator), "data", "sandboxEvil"), false},
		{filepath.Join(string(filepath.Separator), "etc", "passwd"), false},
	}
	for _, tc := range cases {
		if got := within(root, tc.path); got != tc.valid {
			t.Errorf("within(%q, %q) = %v, want %v", root, tc.path, got, tc.valid)
		}
	}
}
