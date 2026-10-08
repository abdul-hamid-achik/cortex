package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunReplaysDecisions(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"-limit", "2", "-window", "1000", "0", "100", "200", "?300"}, &out)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	for _, want := range []string{"t=0 allow remaining=1", "t=100 allow remaining=0", "t=200 deny retry-after=800", "t=300 remaining=0"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q: %q", want, got)
		}
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-limit", "0"}, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
