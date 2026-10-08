package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrintsSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fetchd.conf")
	if err := os.WriteFile(path, []byte("addr = up:9\ntimeout = 5s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run([]string{"-config", path}, &out); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "timeout: 5s") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunRequiresConfig(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
