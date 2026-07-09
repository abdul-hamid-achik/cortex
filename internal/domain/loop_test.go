package domain

import "testing"

func TestLoopStageIndexOf(t *testing.T) {
	if len(LoopStages) != 6 {
		t.Fatalf("expected 6 loop stages, got %d", len(LoopStages))
	}
	cases := map[Phase]int{
		PhaseNew:                0,
		PhaseOrienting:          0,
		PhaseInvestigating:      1,
		PhasePlanned:            2,
		PhaseChanging:           3,
		PhaseVerifying:          4,
		PhasePersisting:         5,
		PhaseComplete:           5,
		PhaseBlocked:            -1,
		PhaseAbandoned:          -1,
		PhaseNeedsHumanDecision: -1,
	}
	for phase, want := range cases {
		if got := LoopStageIndexOf(phase); got != want {
			t.Errorf("LoopStageIndexOf(%s) = %d, want %d", phase, got, want)
		}
	}
}
