/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d":  7 * 24 * time.Hour,
		"1d":  24 * time.Hour,
		"24h": 24 * time.Hour,
		"90m": 90 * time.Minute,
	} {
		got, err := parseAge(in)
		if err != nil || got != want {
			t.Errorf("parseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0d", "-1d", "d", "0h", "abc", "1.5d"} {
		if _, err := parseAge(bad); err == nil {
			t.Errorf("parseAge(%q) should error", bad)
		}
	}
}

func TestCLIPruneDefaultsToDryRun(t *testing.T) {
	ws := cliRepo(t)
	_ = startTask(t, ws)
	out, err := runCLI(t, "--json", "prune", "--older-than", "24h")
	if err != nil {
		t.Fatalf("prune --json: %v (%s)", err, out)
	}
	var rep map[string]any
	if e := json.Unmarshal([]byte(out), &rep); e != nil {
		t.Fatalf("prune --json not JSON: %s", out)
	}
	if rep["applied"] != false {
		t.Errorf("prune default should be a dry run (applied=false), got: %v", rep)
	}
	if _, ok := rep["stale"]; !ok {
		t.Errorf("prune report should carry a stale list, got: %v", rep)
	}
}
