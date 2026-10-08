package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRetriesGet(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-method", "GET", "-script", "503,200"}, &out); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "final: 200 attempts=2 waited=100ms") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunRejectsBadScript(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-script", "banana"}, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
