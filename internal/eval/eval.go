// Package eval is Cortex's evaluation harness: a small benchmark of
// real task types where success requires BOTH a correct outcome AND an adequate
// evidence trail. Each scenario drives a real kernel through the lifecycle in a
// throwaway git workspace and scores the result. Scenarios needing live external
// tooling (browser/video/secret backends) declare Requires and are skipped when
// those tools are absent, so the harness runs anywhere.
package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// Scenario is one benchmark case.
type Scenario struct {
	Name     string
	Category string
	Requires []string // external binaries needed; scenario is skipped if any is absent
	Pending  bool     // the slot is wired but its assertions aren't authored yet
	// Run builds its own workspace (so it controls the seed files + adapters),
	// drives the lifecycle, and returns findings — an empty slice means the
	// scenario passed (correct outcome AND an adequate evidence trail).
	Run func(t *testing.T) []string
}

// Score is a scenario's outcome.
type Score struct {
	Name     string
	Category string
	Skipped  bool
	Reason   string
	Findings []string
}

func (s Score) Passed() bool { return !s.Skipped && len(s.Findings) == 0 }

// Env is a scenario's sandbox: a fresh git workspace and a kernel wired with the
// chosen adapters.
type Env struct {
	t     *testing.T
	dir   string
	k     *kernel.Kernel
	ctx   context.Context
	tasks map[string]bool
}

// NewEnv builds a throwaway git repo and a kernel with the given adapters (git is
// always included). files seeds the repo before the initial commit.
func NewEnv(t *testing.T, files map[string]string, extra ...adapters.Adapter) *Env {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "eval@eval.co")
	gitRun(t, dir, "config", "user.name", "eval")
	for path, body := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "seed")

	cfg := config.For(dir)
	// Keep each scenario's sandbox self-contained and repo-local under its temp
	// workspace (cases now default to a central XDG tree; pin them back here so
	// scenarios stay hermetic and auto-cleaned with t.TempDir).
	cfg.CasesDir = filepath.Join(dir, ".cortex", "cases")
	store, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	// Cortex must git-ignore its own case-file state, or every write shows up as a
	// workspace change and floods scope-drift. Same helper the kernel uses.
	config.EnsureStateIgnored(dir, cfg.CasesDir)
	all := append([]adapters.Adapter{adapters.NewGit()}, extra...)
	return &Env{t: t, dir: dir, k: kernel.NewWith(cfg, store, adapters.NewRegistry(all...)), ctx: context.Background(), tasks: map[string]bool{}}
}

// Write overwrites a file in the workspace (an in-flight edit).
func (e *Env) Write(path, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, path), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *Env) Kernel() *kernel.Kernel { return e.k }
func (e *Env) Ctx() context.Context   { return e.ctx }

// Metrics returns the observability metrics for a task (the evidence-trail score).
func (e *Env) Metrics(taskID string) kernel.TaskMetrics {
	m, err := e.k.TaskMetrics(taskID)
	if err != nil {
		e.t.Fatalf("metrics: %v", err)
	}
	return m
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// RunAll executes every scenario and returns a scorecard.
func RunAll(t *testing.T, scenarios []Scenario) []Score {
	t.Helper()
	scores := make([]Score, 0, len(scenarios))
	for _, sc := range scenarios {
		s := Score{Name: sc.Name, Category: sc.Category}
		if sc.Pending {
			s.Skipped = true
			s.Reason = "scenario not yet authored"
			scores = append(scores, s)
			continue
		}
		if missing := missingTools(sc.Requires); missing != "" {
			s.Skipped = true
			s.Reason = "requires " + missing
			scores = append(scores, s)
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.Findings = append(s.Findings, fmt.Sprintf("panicked: %v", r))
				}
			}()
			s.Findings = sc.Run(t)
		}()
		scores = append(scores, s)
	}
	return scores
}

func missingTools(reqs []string) string {
	for _, r := range reqs {
		if _, err := exec.LookPath(r); err != nil {
			return r
		}
	}
	return ""
}

// fakeAdapter is a configurable in-package adapter so scenarios can pin a tool's
// behavior (e.g. a passing structural review, or a misleading search hit)
// without the real binary.
type fakeAdapter struct {
	name string
	caps []adapters.Capability
	byOp map[string]adapters.Result
	def  adapters.Result
}

func (f *fakeAdapter) Name() string                        { return f.name }
func (f *fakeAdapter) Capabilities() []adapters.Capability { return f.caps }
func (f *fakeAdapter) Health(context.Context) error        { return nil }
func (f *fakeAdapter) Execute(_ context.Context, req adapters.Request) (adapters.Result, error) {
	r := f.def
	if op, ok := f.byOp[req.Operation]; ok {
		r = op
	}
	r.Tool = f.name
	return r, nil
}

// codemapPass is a codemap fake whose review/impact are authoritative — used by
// scenarios that need a passing structural verifier.
func codemapPass() adapters.Adapter {
	authoritative := adapters.Result{Status: adapters.StatusAuthoritative,
		Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed; blast radius resolved", Confidence: "high"}}}
	return &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		def: authoritative, byOp: map[string]adapters.Result{"review": authoritative, "impact": authoritative}}
}

// vecgrepMisleading is a vecgrep fake that returns a plausible-but-wrong,
// low-confidence candidate for the misleading-search case.
func vecgrepMisleading() adapters.Adapter {
	return &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		def: adapters.Result{Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
			{Kind: "semantic_search", Confidence: "low", Claim: "unrelated helper looks similar (score 0.51)",
				Location: &adapters.Location{File: "src/unrelated.go"}}}}}
}

var _ = domain.PhaseComplete // keep domain imported for scenario helpers
