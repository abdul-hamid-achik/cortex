package kernel

import (
	"context"
	"sync"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
)

// racingAdapter returns a fixed result and, the first time it runs, lets a
// concurrent writer bump the case revision mid-round (a note, a decision, a
// lease renewal, or a background job all do this in practice).
type racingAdapter struct {
	name   string
	result adapters.Result
	race   func()
	once   sync.Once
}

func (r *racingAdapter) Name() string                        { return r.name }
func (r *racingAdapter) Capabilities() []adapters.Capability { return nil }
func (r *racingAdapter) Health(context.Context) error        { return nil }
func (r *racingAdapter) Execute(context.Context, adapters.Request) (adapters.Result, error) {
	r.once.Do(r.race)
	return r.result, nil
}

func TestInvestigateSurvivesAConcurrentCaseWrite(t *testing.T) {
	ws := testRepo(t)
	vecgrep := &racingAdapter{name: "vecgrep", result: adapters.Result{
		Tool: "vecgrep", Operation: "search", Status: adapters.StatusAuthoritative,
		Facts: []adapters.Fact{{Kind: "semantic_search", Claim: "HandleCallback in src/callback.go", Confidence: "medium",
			Location: &adapters.Location{File: "src/callback.go", StartLine: 1, Symbol: "HandleCallback"}}},
	}}
	k := newTestKernel(t, ws, vecgrep)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "find the callback", Mode: "investigate"})
	vecgrep.race = func() {
		c, err := k.Store().Load(started.TaskID)
		if err != nil {
			t.Errorf("race load: %v", err)
			return
		}
		c.Notes = append(c.Notes, "concurrent operator note")
		if err := k.Store().Save(c); err != nil {
			t.Errorf("race save: %v", err)
		}
	}
	before, _ := k.Store().Evidence(started.TaskID)

	env, err := k.Investigate(context.Background(), InvestigateInput{TaskID: started.TaskID, Question: "where is HandleCallback"})
	if err != nil || !env.OK {
		t.Fatalf("a concurrent case write must not fail an investigation round: %+v %v", env, err)
	}
	c, _ := k.Store().Load(started.TaskID)
	if c.InvestigationRounds != 1 {
		t.Fatalf("the round must still count against the budget, rounds=%d", c.InvestigationRounds)
	}
	kept := false
	for _, note := range c.Notes {
		kept = kept || note == "concurrent operator note"
	}
	if !kept {
		t.Fatalf("the concurrent writer's note was overwritten: %v", c.Notes)
	}
	after, _ := k.Store().Evidence(started.TaskID)
	if got := len(after) - len(before); got != len(env.Facts) {
		t.Fatalf("evidence ledger grew by %d but the round reported %d facts", got, len(env.Facts))
	}
}
