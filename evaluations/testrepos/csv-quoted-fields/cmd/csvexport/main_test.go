package main

import (
	"bytes"
	"testing"
)

func TestRunPrintsRecord(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"id", "a,b", "plain"}, &out); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if want := "id,\"a,b\",plain\n"; out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestRunNeedsFields(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
