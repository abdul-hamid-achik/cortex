package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestInvestigateReviewModeSkipsOpenSemanticSearch(t *testing.T) {
	ws := testRepo(t)
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		byOp: map[string]adapters.Result{
			"search": {Status: adapters.StatusAuthoritative, Summary: "should not run",
				Facts: []adapters.Fact{{Kind: "semantic_search", Claim: "noise", Confidence: "low"}}},
		}}
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		byOp: map[string]adapters.Result{
			"review": {Status: adapters.StatusAuthoritative, Summary: "review ok",
				Facts: []adapters.Fact{{Kind: "code_location", Claim: "changed symbol Foo", Confidence: "medium",
					Location: &adapters.Location{File: "internal/foo.go"}}}},
			"status": {Status: adapters.StatusAuthoritative},
		}}
	k := newTestKernel(t, ws, cm, vg)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "review branch", Mode: domain.ModeReview})
	if err != nil || !started.OK {
		t.Fatalf("start: %v %+v", err, started)
	}
	env, err := k.Investigate(context.Background(), InvestigateInput{
		TaskID: started.TaskID, Question: "review the changes to worker/src/jobs/profile.ts",
	})
	if err != nil || !env.OK {
		t.Fatalf("investigate: %v %+v", err, env)
	}
	for _, req := range vg.requests() {
		if req.Operation == "search" {
			t.Fatalf("review mode must not call open vecgrep search by default, got %+v", req)
		}
	}
	foundReview := false
	for _, req := range cm.requests() {
		if req.Operation == "review" {
			foundReview = true
		}
	}
	if !foundReview {
		t.Fatal("review mode should call codemap review")
	}
	if !strings.Contains(strings.Join(env.Warnings, " "), "review mode") {
		t.Errorf("want review-mode warning, got %v", env.Warnings)
	}
}

func TestStampResultsDropsJunkPaths(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "filter junk"})
	if err != nil || !started.OK {
		t.Fatalf("start: %v %+v", err, started)
	}
	c, _ := k.store.Load(started.TaskID)
	res := []adapters.Result{{
		Tool: "vecgrep", Operation: "search", Status: adapters.StatusAuthoritative,
		Facts: []adapters.Fact{
			{Kind: "semantic_search", Claim: "generic in .agent/cases/x/commands.jsonl", Confidence: "low",
				Location: &adapters.Location{File: ".agent/cases/x/commands.jsonl"}},
			{Kind: "semantic_search", Claim: "fn in worker/src/jobs/profile.ts", Confidence: "low",
				Location: &adapters.Location{File: "worker/src/jobs/profile.ts"}},
		},
	}}
	facts, warnings, _, err := k.stampResults(c, res, 10, "test", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("want 1 kept fact, got %d: %+v", len(facts), facts)
	}
	if !strings.Contains(facts[0].Claim, "profile.ts") {
		t.Fatalf("kept wrong fact: %+v", facts[0])
	}
	if !strings.Contains(strings.Join(warnings, " "), "junk paths") {
		t.Errorf("want junk-path warning, got %v", warnings)
	}
}

func TestSeedFromNotesStampsOrientation(t *testing.T) {
	ws := testRepo(t)
	note := filepath.Join(ws, "SEED.md")
	if err := os.WriteFile(note, []byte("# OPG-14827\n\nShip Task Profile fan-out.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := newTestKernel(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{
		Goal: "seeded review", Mode: domain.ModeReview, SeedPaths: []string{"SEED.md"},
	})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %+v", err, env)
	}
	found := false
	for _, f := range env.Facts {
		if strings.Contains(f.Claim, "SEED.md") || strings.Contains(f.Claim, "fan-out") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want seeded note fact, got %+v", env.Facts)
	}
}
