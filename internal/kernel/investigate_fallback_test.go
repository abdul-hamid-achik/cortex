package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// commitFile adds a tracked file to the test repo so `git grep` (which searches
// tracked files only) can find it.
func commitFile(t *testing.T, ws, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "add " + rel}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

func dumpEvidence(evs []domain.Evidence) string {
	var b strings.Builder
	for _, e := range evs {
		file := ""
		if e.Location != nil {
			file = e.Location.File
		}
		b.WriteString(e.Source.Tool + " | " + string(e.Kind) + " | " + file + " | " + e.Claim + "\n")
	}
	return b.String()
}

func TestInvestigateFallsBackToGitGrepWhenSemanticDiscoveryUnavailable(t *testing.T) {
	ws := testRepo(t)
	commitFile(t, ws, filepath.Join("src", "refund.go"), "package src\n\nvar quicksilver = 1\n")

	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"search": {Status: adapters.StatusUnavailable, Summary: "not_indexed: run vecgrep index"},
		}}
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, vg, cm)

	env, err := k.StartTask(context.Background(), StartInput{Goal: "fix refund", Surfaces: []domain.Surface{domain.SurfaceCode}})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %+v", err, env)
	}
	res, err := k.Investigate(context.Background(), InvestigateInput{TaskID: env.TaskID, Question: "where is quicksilver used"})
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}

	evs, err := k.Store().Evidence(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range evs {
		if e.Source.Tool == "git" && e.Kind == domain.KindCodeLocation && e.Location != nil && e.Location.File == "src/refund.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a git-grep candidate for src/refund.go; evidence:\n%s", dumpEvidence(evs))
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "git grep") {
		t.Errorf("expected a git-grep fallback warning, got: %v", res.Warnings)
	}
}

func TestInvestigateNoGitGrepFallbackWhenSemanticSearchAuthoritative(t *testing.T) {
	ws := testRepo(t)
	commitFile(t, ws, filepath.Join("src", "refund.go"), "package src\n\nvar quicksilver = 1\n")

	// Semantic search runs and returns an authoritative (empty) result — a real
	// "nothing found", which the fallback must NOT paper over with literal noise.
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, vg, cm)

	env, err := k.StartTask(context.Background(), StartInput{Goal: "fix refund", Surfaces: []domain.Surface{domain.SurfaceCode}})
	if err != nil || !env.OK {
		t.Fatalf("start: %v", err)
	}
	res, err := k.Investigate(context.Background(), InvestigateInput{TaskID: env.TaskID, Question: "where is quicksilver used"})
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}

	evs, _ := k.Store().Evidence(env.TaskID)
	for _, e := range evs {
		if e.Source.Tool == "git" && e.Location != nil && e.Location.File == "src/refund.go" {
			t.Fatalf("git-grep fallback must not run when semantic search is authoritative: %+v", e)
		}
	}
	if strings.Contains(strings.Join(res.Warnings, " "), "fell back to a literal git grep") {
		t.Errorf("no fallback warning expected, got: %v", res.Warnings)
	}
}

func TestSemanticDiscoveryUnavailable(t *testing.T) {
	steps := []step{
		{tool: "vecgrep", op: "memory_recall"},
		{tool: "vecgrep", op: "search"},
		{tool: "codemap", op: "find"},
	}
	results := []adapters.Result{
		{Tool: "vecgrep", Status: adapters.StatusAuthoritative},
		{Tool: "vecgrep", Status: adapters.StatusUnavailable},
		{Tool: "codemap", Status: adapters.StatusAuthoritative},
	}
	if !semanticDiscoveryUnavailable(steps, results) {
		t.Error("an unavailable vecgrep search should trigger the fallback")
	}
	results[1].Status = adapters.StatusAuthoritative
	if semanticDiscoveryUnavailable(steps, results) {
		t.Error("an authoritative vecgrep search must not trigger the fallback")
	}
	noSearch := []step{{tool: "codemap", op: "impact"}}
	if semanticDiscoveryUnavailable(noSearch, []adapters.Result{{Tool: "codemap", Status: adapters.StatusUnavailable}}) {
		t.Error("a route with no vecgrep search step must not trigger the fallback")
	}
}

func TestGrepPatternPrefersIdentifierThenLongest(t *testing.T) {
	for question, want := range map[string]string{
		"where is HandleCallback defined": "HandleCallback", // camelCase identifier wins
		"how does sha256 hashing work":    "sha256",         // digit-bearing identifier wins over a longer word
		"where is the quicksilver used":   "quicksilver",    // longest non-stopword
		"what is the return url":          "return",         // longest ("url" is too short to win)
		"how does it work":                "",               // only stopwords/interrogatives remain
	} {
		if got := grepPattern(question); got != want {
			t.Errorf("grepPattern(%q) = %q, want %q", question, got, want)
		}
	}
}
