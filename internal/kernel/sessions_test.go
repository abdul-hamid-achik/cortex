package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

func TestSessionStaleSince(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	fresh := SessionSummary{Active: true, UpdatedAt: now.Add(-1 * time.Hour)}
	old := SessionSummary{Active: true, UpdatedAt: now.Add(-48 * time.Hour)}
	done := SessionSummary{Active: false, UpdatedAt: now.Add(-48 * time.Hour)}
	if fresh.StaleSince(now, 24*time.Hour) {
		t.Error("a 1h-old in-flight session should not be stale at 24h")
	}
	if !old.StaleSince(now, 24*time.Hour) {
		t.Error("a 48h-old in-flight session should be stale at 24h")
	}
	if done.StaleSince(now, 24*time.Hour) {
		t.Error("a terminal session is never stale")
	}
	if old.StaleSince(now, 0) {
		t.Error("a zero age disables the stale check")
	}
}

// repoNamed inits a git repo at a subdir with a controlled basename, so its
// slug is predictable and distinct (unlike t.TempDir's numeric basenames).
func repoNamed(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t.co"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "f.go"), []byte("package a\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "i"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		_ = cmd.Run()
	}
	return dir
}

// kernelAt builds a kernel for ws WITHOUT overriding CORTEX_HOME, so multiple
// kernels can share one central state tree (newTestKernel isolates each call).
func kernelAt(t *testing.T, ws string) *Kernel {
	t.Helper()
	cfg := config.For(ws)
	store, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	return NewWith(cfg, store, adapters.NewRegistry(adapters.NewGit()))
}

func TestAllSessionsCrossWorkspace(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir()) // one central tree shared by both repos
	wsA := repoNamed(t, "alpha")
	wsB := repoNamed(t, "beta")

	ctx := context.Background()
	if _, err := kernelAt(t, wsA).StartTask(ctx, StartInput{Goal: "fix redirect in alpha", Surfaces: []domain.Surface{domain.SurfaceCode}}); err != nil {
		t.Fatal(err)
	}
	if _, err := kernelAt(t, wsB).StartTask(ctx, StartInput{Goal: "index bug in beta", Surfaces: []domain.Surface{domain.SurfaceCode}}); err != nil {
		t.Fatal(err)
	}

	all, err := AllSessions(SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions across workspaces, got %d: %+v", len(all), all)
	}
	slugs := map[string]bool{}
	for _, s := range all {
		slugs[s.Slug] = true
		if s.ID == "" || s.Goal == "" {
			t.Errorf("incomplete summary: %+v", s)
		}
		if !s.Active {
			t.Errorf("a fresh session should be active (non-terminal), got phase %s", s.Phase)
		}
	}
	if !slugs["alpha"] || !slugs["beta"] {
		t.Errorf("expected slugs alpha+beta, got %v", slugs)
	}

	// Repo filter narrows to one workspace by slug substring.
	only, err := AllSessions(SessionFilter{Repo: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Slug != "alpha" {
		t.Fatalf("repo filter should return only alpha, got %+v", only)
	}
}

func TestAllSessionsEmpty(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	all, err := AllSessions(SessionFilter{})
	if err != nil {
		t.Fatalf("empty sessions root should not error: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected no sessions, got %d", len(all))
	}
}

func TestAllSessionsFailsClosedWhenActiveVerificationIsStale(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "freshness")
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	cfg := config.For(ws)
	store, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	k := NewWith(cfg, store, adapters.NewRegistry(adapters.NewGit(), codemap))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "keep compact status current", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"f.go"}}, Uncertainty: "u",
	})
	if err := os.WriteFile(filepath.Join(ws, "f.go"), []byte("package a\nvar A = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !verified.OK {
		t.Fatalf("verify = %+v", verified)
	}
	all, err := AllSessions(SessionFilter{})
	if err != nil || len(all) != 1 || all[0].VerificationOutcome != VerificationVerified {
		t.Fatalf("fresh compact session = %+v (%v)", all, err)
	}

	if err := os.WriteFile(filepath.Join(ws, "f.go"), []byte("package a\nvar A = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err = AllSessions(SessionFilter{})
	if err != nil || len(all) != 1 {
		t.Fatalf("stale compact session = %+v (%v)", all, err)
	}
	if all[0].VerificationOutcome == VerificationVerified || all[0].Verified != 0 {
		t.Fatalf("active compact session presented stale proof as current: %+v", all[0])
	}
}

func TestAllSessionsKeepsTerminalVerificationHistorical(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "history")
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	cfg := config.For(ws)
	store, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	k := NewWith(cfg, store, adapters.NewRegistry(adapters.NewGit(), codemap))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "preserve compact history", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"f.go"}}, Uncertainty: "u",
	})
	if err := os.WriteFile(filepath.Join(ws, "f.go"), []byte("package a\nvar A = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	remembered, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "done"})
	if !remembered.OK {
		t.Fatalf("remember = %+v", remembered)
	}
	if err := os.WriteFile(filepath.Join(ws, "f.go"), []byte("package a\nvar A = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := AllSessions(SessionFilter{})
	if err != nil || len(all) != 1 || all[0].VerificationOutcome != VerificationVerified {
		t.Fatalf("terminal history was retroactively invalidated: %+v (%v)", all, err)
	}
}
