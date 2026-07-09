package kernel

import (
	"context"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestTimelineAfterStart(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "gamma")
	env, err := kernelAt(t, ws).StartTask(context.Background(),
		StartInput{Goal: "trace me", Surfaces: []domain.Surface{domain.SurfaceCode}})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}

	entries, err := Timeline(env.TaskID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected timeline entries")
	}
	kinds := map[string]int{}
	for _, e := range entries {
		kinds[e.Kind]++
	}
	// StartTask walks new→orienting→investigating: two recorded transitions.
	if kinds["phase"] < 2 {
		t.Errorf("expected >=2 phase transitions, got %d (%v)", kinds["phase"], kinds)
	}
	if kinds["evidence"] == 0 {
		t.Errorf("expected orientation evidence in the timeline (%v)", kinds)
	}
	// Chronologically ascending.
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp.Before(entries[i-1].Timestamp) {
			t.Errorf("timeline not sorted at index %d", i)
		}
	}
}

func TestTimelineNotFound(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	if _, err := Timeline("task_does_not_exist"); err == nil {
		t.Error("expected an error locating an unknown session")
	}
}

func TestAbortRecordsPhase(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "delta")
	k := kernelAt(t, ws)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "abort me"})
	if _, err := k.AbortTask(env.TaskID, "superseded"); err != nil {
		t.Fatal(err)
	}
	entries, err := Timeline(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Kind == "phase" && e.Summary == "investigating → abandoned" {
			found = true
		}
	}
	if !found {
		t.Errorf("abort should record a phase transition to abandoned; entries=%v", entries)
	}
}
