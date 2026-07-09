package kernel

import (
	"context"
	"testing"
)

func TestShowSession(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "zeta")
	env, err := kernelAt(t, ws).StartTask(context.Background(), StartInput{Goal: "inspect me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}

	v, err := ShowSession(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Case == nil || v.Case.Goal != "inspect me" {
		t.Fatalf("case not loaded: %+v", v.Case)
	}
	if v.Slug != "zeta" {
		t.Errorf("slug = %s, want zeta", v.Slug)
	}
	if len(v.Timeline) == 0 {
		t.Error("expected timeline entries")
	}
	if len(v.PhaseDurations) == 0 || v.ElapsedMs <= 0 {
		t.Errorf("expected phase durations + positive elapsed, got %d durs / %dms", len(v.PhaseDurations), v.ElapsedMs)
	}
}

func TestShowSessionNotFound(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	if _, err := ShowSession("task_does_not_exist"); err == nil {
		t.Error("expected an error for an unknown session")
	}
}
