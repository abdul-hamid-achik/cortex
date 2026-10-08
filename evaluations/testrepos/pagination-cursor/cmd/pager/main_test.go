package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsPages(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-total", "5", "-size", "2"}, &out); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "page 1: item-1 item-2") {
		t.Fatalf("output missing first page: %q", out.String())
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-bogus"}, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
