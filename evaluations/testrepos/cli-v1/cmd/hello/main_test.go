package main

import (
	"strings"
	"testing"
)

func TestHelpContract(t *testing.T) {
	code, output := help([]string{"--help"})
	if code != 0 {
		t.Fatalf("--help exit code = %d, want 0", code)
	}
	if !strings.Contains(output, "--json") {
		t.Fatalf("--help does not document --json: %q", output)
	}
}
