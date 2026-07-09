package domain

// LoopStage is one step of the six-step reasoning loop (SPEC §3.1). Several
// phases can share a stage — e.g. new and orienting both sit at "orient".
type LoopStage struct {
	Label  string
	Phases []Phase
}

// LoopStages is the canonical, ordered reasoning loop. It is the single source
// both surfaces render as a progress track (studio board + `cortex status`).
// Terminal-bad phases (blocked / abandoned / needs_human_decision) belong to no
// stage and map to index -1.
var LoopStages = []LoopStage{
	{"orient", []Phase{PhaseNew, PhaseOrienting}},
	{"inv", []Phase{PhaseInvestigating}},
	{"plan", []Phase{PhasePlanned}},
	{"change", []Phase{PhaseChanging}},
	{"verify", []Phase{PhaseVerifying}},
	{"keep", []Phase{PhasePersisting, PhaseComplete}},
}

// LoopStageIndexOf returns the index into LoopStages of the stage a phase sits
// at, or -1 for a terminal-bad or unknown phase.
func LoopStageIndexOf(p Phase) int {
	for i, s := range LoopStages {
		for _, ph := range s.Phases {
			if ph == p {
				return i
			}
		}
	}
	return -1
}
