package domain

import "testing"

func TestCanTransition_LegalPath(t *testing.T) {
	// The happy path through the lifecycle must be legal at every step.
	path := []Phase{PhaseNew, PhaseOrienting, PhaseInvestigating, PhasePlanned, PhaseChanging, PhaseVerifying, PhasePersisting, PhaseComplete}
	for i := 0; i+1 < len(path); i++ {
		if !CanTransition(path[i], path[i+1]) {
			t.Errorf("expected %s → %s to be legal", path[i], path[i+1])
		}
	}
}

func TestCanTransition_Illegal(t *testing.T) {
	illegal := [][2]Phase{
		{PhaseNew, PhaseComplete},           // cannot skip the whole lifecycle
		{PhaseInvestigating, PhaseChanging}, // must plan before changing
		{PhaseComplete, PhaseChanging},      // terminal is terminal
		{PhaseNew, PhaseVerifying},
		{Phase("corrupt"), PhaseNeedsHumanDecision}, // a wait cannot legitimize an unknown phase
	}
	for _, tc := range illegal {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("expected %s → %s to be illegal", tc[0], tc[1])
		}
	}
}

func TestCanTransition_InvestigateOnlySkipsChanging(t *testing.T) {
	// An investigate-only task may go planned → verifying without changing.
	if !CanTransition(PhasePlanned, PhaseVerifying) {
		t.Error("planned → verifying should be legal for investigate-only tasks")
	}
}

func TestCanTransition_FailedVerifyLoopsBack(t *testing.T) {
	if !CanTransition(PhaseVerifying, PhaseChanging) {
		t.Error("verifying → changing should be legal so a failed verify can loop back")
	}
}

func TestBlockedReachableFromAnyNonTerminal(t *testing.T) {
	for _, p := range []Phase{PhaseNew, PhaseInvestigating, PhasePlanned, PhaseVerifying} {
		if !CanTransition(p, PhaseBlocked) {
			t.Errorf("blocked should be reachable from %s", p)
		}
	}
	if CanTransition(PhaseComplete, PhaseBlocked) {
		t.Error("blocked must NOT be reachable from a terminal phase")
	}
}

func TestPhaseIsTerminal(t *testing.T) {
	terminal := []Phase{PhaseComplete, PhaseBlocked, PhaseAbandoned}
	for _, p := range terminal {
		if !p.IsTerminal() {
			t.Errorf("%s should be terminal", p)
		}
	}
	if PhaseNeedsHumanDecision.IsTerminal() {
		t.Error("needs_human_decision is a resumable wait, not a terminal phase")
	}
	if PhaseInvestigating.IsTerminal() {
		t.Error("investigating is not terminal")
	}
}

func TestHumanDecisionWaitCanResumeActivePhase(t *testing.T) {
	for _, phase := range []Phase{PhaseInvestigating, PhasePlanned, PhaseChanging, PhaseVerifying, PhasePersisting} {
		if !CanTransition(phase, PhaseNeedsHumanDecision) {
			t.Errorf("%s should be able to pause for a decision", phase)
		}
		if !CanTransition(PhaseNeedsHumanDecision, phase) {
			t.Errorf("decision wait should structurally allow resume to %s", phase)
		}
	}
	if CanTransition(PhaseNeedsHumanDecision, PhaseComplete) {
		t.Error("decision wait must not jump directly to complete")
	}
}

func TestChangeBoundaryDeclared(t *testing.T) {
	if (ChangeBoundary{}).Declared() {
		t.Error("empty boundary should not be declared")
	}
	if !(ChangeBoundary{Files: []string{"a.go"}}).Declared() {
		t.Error("boundary with a file should be declared")
	}
	if !(ChangeBoundary{Symbols: []string{"Foo"}}).Declared() {
		t.Error("boundary with a symbol should be declared")
	}
}

func TestCaseFileHasSurface(t *testing.T) {
	c := &CaseFile{Surfaces: []Surface{SurfaceCode, SurfaceBrowser}}
	if !c.HasSurface(SurfaceBrowser) {
		t.Error("expected browser surface")
	}
	if c.HasSurface(SurfaceTerminal) {
		t.Error("did not expect terminal surface")
	}
}

func TestModeAndSurfaceValidation(t *testing.T) {
	for _, mode := range []Mode{ModeChange, ModeInvestigate, ModeReview} {
		if !mode.Valid() {
			t.Errorf("known mode %q should be valid", mode)
		}
	}
	if Mode("mutate").Valid() || Mode("").Valid() {
		t.Error("unknown/empty modes must not be valid domain values")
	}

	for _, surface := range []Surface{SurfaceCode, SurfaceBrowser, SurfaceTerminal, SurfaceArtifact, SurfaceSecret} {
		if !surface.Valid() {
			t.Errorf("known surface %q should be valid", surface)
		}
	}
	if Surface("desktop").Valid() || Surface("").Valid() {
		t.Error("unknown/empty surfaces must not be valid domain values")
	}
}
