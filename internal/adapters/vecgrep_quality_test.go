package adapters

import (
	"context"
	"strings"
	"testing"
)

// ---- vecgrep discovery-quality gates (dogfooding 2026-07-15: a deep
// investigation recorded 16 low-confidence facts whose claims were markdown
// headings ("# Cartographer") and bare import lines, all at scores 0.01–0.02) ----

func TestVecgrepFiltersLowValueChunks(t *testing.T) {
	// Heading-only and import-only chunks are filtered before they become
	// facts; the substantive chunk survives and a warning counts the drops.
	fixture := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":100},"hits":[
	  {"relative_path":"README.md","start_line":1,"content":"# Cartographer","score":0.55},
	  {"relative_path":"apps/web/src/app/page.tsx","start_line":1,"content":"import { createDemoDashboardData } from \"@cartographer/domain\"","score":0.52},
	  {"relative_path":"AGENTS.md","start_line":40,"content":"### Jobs app","score":0.50},
	  {"relative_path":"packages/domain/src/demo-data.ts","symbol_name":"createDemoDashboardData","start_line":10,"content":"export function createDemoDashboardData(): DashboardData {","score":0.61}]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "fixture data"}})
	if res.Status != StatusAuthoritative {
		t.Fatalf("status = %s", res.Status)
	}
	if len(res.Facts) != 1 {
		t.Fatalf("expected 1 substantive fact after filtering, got %d: %s", len(res.Facts), factClaims(res))
	}
	if !strings.Contains(res.Facts[0].Claim, "createDemoDashboardData") {
		t.Errorf("the surviving fact should be the substantive chunk, got: %s", res.Facts[0].Claim)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "3 low-value hits") {
		t.Errorf("filtered hits should be surfaced in a warning, got: %v", res.Warnings)
	}
}

func TestVecgrepAllWeakScoresIsNoStrongCandidates(t *testing.T) {
	// When EVERY hit scores below the usefulness floor, the search says
	// "no strong candidates" instead of recording a pile of weak facts.
	fixture := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":100},"hits":[
	  {"relative_path":"a.go","start_line":1,"content":"func A() error { return doWork() }","score":0.02},
	  {"relative_path":"b.go","start_line":9,"content":"func B() { validateState(session) }","score":0.01}]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "campaign state"}})
	if res.Status != StatusAuthoritative {
		t.Fatalf("a weak-but-clean search is still authoritative, got %s", res.Status)
	}
	if len(res.Facts) != 0 {
		t.Fatalf("weak hits must not be recorded as facts, got %d: %s", len(res.Facts), factClaims(res))
	}
	if !strings.Contains(res.Summary, "no strong candidates") {
		t.Errorf("summary should report no strong candidates, got: %s", res.Summary)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "no strong candidates") {
		t.Errorf("warnings should carry the degradation message, got: %v", res.Warnings)
	}
}

func TestVecgrepMixedScoresKeepsAllSubstantiveHits(t *testing.T) {
	// The floor only fires when ALL hits are weak — one strong hit keeps the
	// weak-but-substantive neighbors as ordinary low-confidence candidates.
	fixture := `[
	  {"relative_path":"a.go","start_line":1,"content":"func Strong() {}","score":0.71},
	  {"relative_path":"b.go","start_line":2,"content":"func Weak() {}","score":0.03}]`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "x"}})
	if len(res.Facts) != 2 {
		t.Fatalf("mixed scores should keep both facts, got %d", len(res.Facts))
	}
}

func TestVecgrepUnscoredHitsSkipScoreGate(t *testing.T) {
	// Old binaries / keyword mode emit no score (0). Absence of a score must
	// not read as "weak" — the hits are kept.
	fixture := `[{"relative_path":"a.go","start_line":10,"symbol_name":"Foo"},
	  {"relative_path":"b.go","start_line":5,"chunk_type":"block"}]`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "auth"}})
	if len(res.Facts) != 2 {
		t.Fatalf("unscored hits should not be score-gated, got %d facts", len(res.Facts))
	}
}

func TestLowValueChunk(t *testing.T) {
	for _, tc := range []struct {
		in          string
		markdown    bool
		keepImports bool
		want        bool
	}{
		{"# Cartographer", true, false, true},
		{"## Conventions", true, false, true},
		{"### Jobs app", true, false, true},
		{"import { createDemoDashboardData } from \"@cartographer/domain\"", false, false, true},
		{"from zod import something\nimport os", false, false, true},
		{"---", true, false, true},
		{"---", false, false, true}, // trivial regardless of file type
		{"}", false, false, true},
		{"# Security\n\n## Trust boundary", true, false, true},
		{"", true, false, false}, // absent content (old binary) is not evidence of noise
		{"export function createDemoDashboardData(): DashboardData {", false, false, false},
		{"# Heading\nconst plan = campaignPlanSchema.parse(raw)", true, false, false},
		{"The queue ingress returns deterministic acknowledgements.", true, false, false},
		// '#' outside markdown is a comment or preprocessor directive, not a
		// heading — comment chunks are legitimate semantic evidence.
		{"# Validate the campaign plan before saving\n# Raises ValidationError on bad budget", false, false, false},
		{"#!/usr/bin/env bash\n# rotate the logs nightly", false, false, false},
		{"#define MAX_RETRIES 3\n#define BACKOFF_MS 250", false, false, false},
		{"# deploy pipeline for the jobs service", false, false, false},
		// "use"/"using"/"package" are imports only in statement shape; prose survives.
		{"use std::fmt;", false, false, true},
		{"using System.Text;", false, false, true},
		{"package main", false, false, true},
		{"use the campaign dialog to plan a probe run", false, false, false},
		{"using deterministic fixture data keeps tests stable", false, false, false},
		{"package managers are configured via the lockfile", false, false, false},
		// When the question is about imports, import lines are the evidence.
		{"import { createDemoDashboardData } from \"@cartographer/domain\"", false, true, false},
	} {
		if got := lowValueChunk(tc.in, tc.markdown, tc.keepImports); got != tc.want {
			t.Errorf("lowValueChunk(%q, markdown=%v, keepImports=%v) = %v, want %v", tc.in, tc.markdown, tc.keepImports, got, tc.want)
		}
	}
}

func TestVecgrepKeepsCodeCommentChunks(t *testing.T) {
	// A '#' line is a heading only in markdown. The same shape in Python,
	// shell, or YAML is a comment — semantically matched comment chunks are
	// legitimate evidence and must not be filtered (only the markdown hit is).
	fixture := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":100},"hits":[
	  {"relative_path":"scripts/deploy.py","language":"python","start_line":3,"content":"# Validate the campaign plan before saving\n# Raises ValidationError on bad budget","score":0.61},
	  {"relative_path":"README.md","language":"markdown","start_line":1,"content":"# Cartographer","score":0.60}]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "campaign plan validation"}})
	if len(res.Facts) != 1 {
		t.Fatalf("expected the python comment chunk to survive and the markdown heading to drop, got %d facts: %s", len(res.Facts), factClaims(res))
	}
	if !strings.Contains(res.Facts[0].Claim, "deploy.py") {
		t.Errorf("surviving fact should be the python comment chunk, got: %s", res.Facts[0].Claim)
	}
}

func TestVecgrepKeepsImportsWhenQueryAsksAboutImports(t *testing.T) {
	// "where is X imported" is answered BY import lines — filtering them would
	// discard the exact evidence the caller asked for.
	fixture := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":100},"hits":[
	  {"relative_path":"apps/web/src/app/page.tsx","start_line":1,"content":"import { createDemoDashboardData } from \"@cartographer/domain\"","score":0.66}]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "where is createDemoDashboardData imported"}})
	if len(res.Facts) != 1 {
		t.Fatalf("import lines must be kept for an import question, got %d facts: %s", len(res.Facts), factClaims(res))
	}
}
