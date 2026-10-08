package main

import (
	"bytes"
	"testing"
)

func TestRunPrintsPermalink(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"Hello", "World"}, &out); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != "/posts/hello-world\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunRequiresTitle(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
