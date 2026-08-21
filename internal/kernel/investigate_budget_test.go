package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
)

func TestInvestigateBudgetReturnsPartial(t *testing.T) {
	ws := testRepo(t)
	slow := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		byOp:  map[string]adapters.Result{},
		delay: 2 * time.Second,
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "too late",
			Facts: []adapters.Fact{{Kind: "semantic_search", Claim: "should not win", Confidence: "low"}}},
	}
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		byOp: map[string]adapters.Result{
			"status": {Status: adapters.StatusAuthoritative},
			"find":   {Status: adapters.StatusAuthoritative, Summary: "ok"},
		}}
	k := newTestKernel(t, ws, cm, slow)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "partial investigate"})
	if err != nil || !started.OK {
		t.Fatalf("start: %v %+v", err, started)
	}
	// Force a tight deadline so the slow specialist cannot finish.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	env, err := k.Investigate(ctx, InvestigateInput{
		TaskID: started.TaskID, Question: "where is refund handled", Depth: "quick",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("partial investigate should still OK: %+v", env)
	}
	if !env.Degraded && !hasWarning(env.Warnings, "budget exhausted") && !hasWarning(env.Warnings, "timed out") && !hasWarning(env.Warnings, "degraded") {
		t.Fatalf("want degraded/timeout/budget warning, got degraded=%v warnings=%v", env.Degraded, env.Warnings)
	}
}

func TestInvestigateBudgetDefaultApplied(t *testing.T) {
	if investigateBudget("quick") != 20*time.Second {
		t.Fatalf("quick=%s", investigateBudget("quick"))
	}
	if investigateBudget("standard") != 45*time.Second {
		t.Fatalf("standard=%s", investigateBudget("standard"))
	}
	if investigateBudget("deep") != 90*time.Second {
		t.Fatalf("deep=%s", investigateBudget("deep"))
	}
	ctx, cancel := withInvestigateDeadline(context.Background(), "quick")
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > 20*time.Second || time.Until(dl) < 15*time.Second {
		t.Fatalf("quick deadline = %v ok=%v", dl, ok)
	}
	parent, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer parentCancel()
	child, childCancel := withInvestigateDeadline(parent, "deep")
	defer childCancel()
	pd, _ := parent.Deadline()
	cd, ok := child.Deadline()
	if !ok {
		t.Fatal("expected a deadline")
	}
	// Parent is tighter than deep's 90s — keep the parent deadline.
	if !cd.Equal(pd) {
		t.Fatalf("child deadline %v want parent %v", cd, pd)
	}
	longParent, longCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer longCancel()
	capped, cappedCancel := withInvestigateDeadline(longParent, "quick")
	defer cappedCancel()
	cdl, ok := capped.Deadline()
	if !ok {
		t.Fatal("expected capped deadline")
	}
	if rem := time.Until(cdl); rem > 21*time.Second {
		t.Fatalf("long parent should be capped to quick budget, remaining=%s", rem)
	}
}
