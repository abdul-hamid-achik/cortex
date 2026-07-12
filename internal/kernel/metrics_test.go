package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

func TestPhaseDurations(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []casefs.PhaseEvent{
		{Timestamp: base.Add(1 * time.Second), From: domain.PhaseNew, To: domain.PhaseOrienting},
		{Timestamp: base.Add(3 * time.Second), From: domain.PhaseOrienting, To: domain.PhaseInvestigating},
		{Timestamp: base.Add(9 * time.Second), From: domain.PhaseInvestigating, To: domain.PhasePlanned},
	}
	now := base.Add(11 * time.Second)

	// In-flight: the current (planned) phase counts up to now.
	durs, total := phaseDurations(base, events, false, now)
	got := map[string]int64{}
	for _, d := range durs {
		got[d.Phase] = d.Ms
	}
	for phase, want := range map[string]int64{"new": 1000, "orienting": 2000, "investigating": 6000, "planned": 2000} {
		if got[phase] != want {
			t.Errorf("%s = %dms, want %d", phase, got[phase], want)
		}
	}
	if total != 11000 {
		t.Errorf("total = %d, want 11000", total)
	}

	// Terminal: the last phase is not counted to now.
	if _, totalTerm := phaseDurations(base, events, true, now); totalTerm != 9000 {
		t.Errorf("terminal total = %d, want 9000", totalTerm)
	}

	// No history → nil, 0.
	if d, tot := phaseDurations(base, nil, false, now); d != nil || tot != 0 {
		t.Errorf("empty events should be (nil,0); got (%v,%d)", d, tot)
	}
}

func TestTaskMetricsIncludesPhaseDurations(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "measure me", Surfaces: []domain.Surface{domain.SurfaceCode}})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}
	m, err := k.TaskMetrics(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PhaseDurations) == 0 || m.ElapsedMs <= 0 {
		t.Fatalf("expected phase durations and positive elapsed, got %d durations, elapsed %d", len(m.PhaseDurations), m.ElapsedMs)
	}
	seen := map[string]bool{}
	for _, d := range m.PhaseDurations {
		seen[d.Phase] = true
	}
	if !seen["new"] || !seen["orienting"] {
		t.Errorf("expected new + orienting phases recorded, got %v", seen)
	}
}

func TestTaskMetricsDetectsMemoryReuseFromProvenance(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "measure recalled evidence"})
	if err != nil || !env.OK {
		t.Fatalf("start: %+v %v", env, err)
	}
	if err := k.Store().AppendEvidence(env.TaskID, domain.Evidence{
		ID: "ev_recalled", Timestamp: time.Now().UTC(), Kind: domain.KindModelInference,
		Source: domain.Source{Tool: "veclite"}, Claim: "a related resolved case contradicted this hypothesis",
		Confidence: domain.ConfidenceLow,
	}); err != nil {
		t.Fatal(err)
	}
	m, err := k.TaskMetrics(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !m.MemoryReused {
		t.Fatalf("veclite provenance was not counted as memory reuse: %+v", m)
	}
}
