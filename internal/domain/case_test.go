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
	terminal := []Phase{PhaseComplete, PhaseBlocked, PhaseAbandoned, PhaseNeedsHumanDecision}
	for _, p := range terminal {
		if !p.IsTerminal() {
			t.Errorf("%s should be terminal", p)
		}
	}
	if PhaseInvestigating.IsTerminal() {
		t.Error("investigating is not terminal")
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
