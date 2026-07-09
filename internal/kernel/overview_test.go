package kernel

import (
	"context"
	"testing"
	"time"
)

func TestOverviewCrossRepo(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	wsA := repoNamed(t, "alpha")
	wsB := repoNamed(t, "beta")
	ctx := context.Background()

	if _, err := kernelAt(t, wsA).StartTask(ctx, StartInput{Goal: "a1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := kernelAt(t, wsA).StartTask(ctx, StartInput{Goal: "a2"}); err != nil {
		t.Fatal(err)
	}
	kB := kernelAt(t, wsB)
	env, err := kB.StartTask(ctx, StartInput{Goal: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kB.AbortTask(env.TaskID, "not needed"); err != nil {
		t.Fatal(err)
	}

	o, err := BuildOverview(24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if o.Sessions != 3 {
		t.Errorf("sessions = %d, want 3", o.Sessions)
	}
	if o.Active != 2 { // a1, a2 in-flight; b1 aborted (terminal)
		t.Errorf("active = %d, want 2", o.Active)
	}
	if len(o.Repos) != 2 {
		t.Fatalf("repos = %d, want 2", len(o.Repos))
	}
	// alpha has the most sessions → sorted first.
	if o.Repos[0].Repo != "alpha" || o.Repos[0].Sessions != 2 || o.Repos[0].Active != 2 {
		t.Errorf("expected alpha with 2 sessions / 2 active first, got %+v", o.Repos[0])
	}
}

func TestOverviewEmpty(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	o, err := BuildOverview(24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("empty overview should not error: %v", err)
	}
	if o.Sessions != 0 || len(o.Repos) != 0 {
		t.Errorf("expected empty overview, got %+v", o)
	}
}
