package kernel

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

func TestConcurrentPlansKeepCaseAndCompanionsConsistent(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	started, err := k1.StartTask(context.Background(), StartInput{Goal: "coordinate concurrent plans", Risk: "low"})
	if err != nil || !started.OK {
		t.Fatalf("start: err=%v envelope=%+v", err, started)
	}
	inputs := []struct {
		kernel *Kernel
		input  PlanInput
	}{
		{k1, PlanInput{
			TaskID: started.TaskID,
			Hypotheses: []HypothesisInput{{
				Statement: "plan a explanation", DisproveBy: "disprove plan a",
			}},
			ChangeBoundary: domain.ChangeBoundary{Files: []string{"a.go"}},
			Uncertainty:    "plan a uncertainty",
		}},
		{k2, PlanInput{
			TaskID: started.TaskID,
			Hypotheses: []HypothesisInput{{
				Statement: "plan b explanation", DisproveBy: "disprove plan b",
			}},
			ChangeBoundary: domain.ChangeBoundary{Files: []string{"b.go"}},
			Uncertainty:    "plan b uncertainty",
		}},
	}
	type result struct {
		envelope domain.Envelope
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, len(inputs))
	var wg sync.WaitGroup
	for _, current := range inputs {
		wg.Add(1)
		go func(k *Kernel, input PlanInput) {
			defer wg.Done()
			<-start
			envelope, err := k.Plan(input)
			results <- result{envelope: envelope, err: err}
		}(current.kernel, current.input)
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for got := range results {
		if got.envelope.OK {
			successes++
			if got.err != nil {
				t.Fatalf("successful plan returned error: %v", got.err)
			}
			continue
		}
		if got.err == nil || !errors.Is(got.err, casefs.ErrRevisionConflict) {
			t.Fatalf("failed concurrent plan did not return typed conflict: envelope=%+v err=%v", got.envelope, got.err)
		}
	}
	if successes == 0 {
		t.Fatal("no concurrent plan committed")
	}

	finalCase, err := k1.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	finalPlan, err := k1.Store().LoadPlan(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	finalHypotheses, err := k1.Store().Hypotheses(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if finalCase.Status != domain.PhasePlanned ||
		!reflect.DeepEqual(finalCase.ChangeBoundary, finalPlan.ChangeBoundary) ||
		!reflect.DeepEqual(finalCase.VerificationRequired, finalPlan.VerificationRequired) ||
		!reflect.DeepEqual(finalPlan.Hypotheses, finalHypotheses) {
		t.Fatalf("mixed final plan transaction: case=%+v plan=%+v hypotheses=%+v", finalCase, finalPlan, finalHypotheses)
	}
	events, err := k1.Store().PhaseEvents(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	plannedEvents := 0
	for _, event := range events {
		if event.To == domain.PhasePlanned {
			plannedEvents++
		}
	}
	if plannedEvents != 1 {
		t.Fatalf("planned phase was recorded %d times: %+v", plannedEvents, events)
	}
}

func TestConcurrentResolvePreservesDistinctHypotheses(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	started, err := k1.StartTask(context.Background(), StartInput{Goal: "resolve two explanations", Risk: "low"})
	if err != nil || !started.OK {
		t.Fatalf("start: err=%v envelope=%+v", err, started)
	}
	planned, err := k1.Plan(PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{
			{Statement: "first explanation", DisproveBy: "disprove first"},
			{Statement: "second explanation", DisproveBy: "disprove second"},
		},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"shared.go"}},
		Uncertainty:    "both explanations remain uncertain",
	})
	if err != nil || !planned.OK || len(planned.Hypotheses) != 2 {
		t.Fatalf("plan: err=%v envelope=%+v", err, planned)
	}
	before, err := k1.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents, err := k1.Store().PhaseEvents(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}

	inputs := []struct {
		kernel *Kernel
		input  ResolveInput
	}{
		{k1, ResolveInput{
			TaskID: started.TaskID, HypothesisID: planned.Hypotheses[0].ID,
			Status: "rejected", Reason: "first explanation contradicted",
		}},
		{k2, ResolveInput{
			TaskID: started.TaskID, HypothesisID: planned.Hypotheses[1].ID,
			Status: "challenged", Reason: "second explanation needs revision",
		}},
	}
	type result struct {
		envelope domain.Envelope
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, len(inputs))
	var wg sync.WaitGroup
	for _, current := range inputs {
		wg.Add(1)
		go func(k *Kernel, input ResolveInput) {
			defer wg.Done()
			<-start
			envelope, err := k.Resolve(input)
			results <- result{envelope: envelope, err: err}
		}(current.kernel, current.input)
	}
	close(start)
	wg.Wait()
	close(results)
	for got := range results {
		if got.err != nil || !got.envelope.OK {
			t.Fatalf("concurrent resolve did not converge: envelope=%+v err=%v", got.envelope, got.err)
		}
	}

	after, err := k1.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+2 {
		t.Fatalf("resolve revisions = %d, want %d", after.Revision, before.Revision+2)
	}
	hypotheses, err := k1.Store().Hypotheses(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(map[string]domain.HypothesisStatus, len(hypotheses))
	for _, hypothesis := range hypotheses {
		statuses[hypothesis.ID] = hypothesis.Status
	}
	if statuses[planned.Hypotheses[0].ID] != domain.HypRejected ||
		statuses[planned.Hypotheses[1].ID] != domain.HypChallenged {
		t.Fatalf("distinct concurrent resolutions were lost: %+v", hypotheses)
	}
	evidence, err := k1.Store().Evidence(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	resolutionEvidence := make(map[string]int, 2)
	for _, record := range evidence {
		if record.Kind != domain.KindHumanReport {
			continue
		}
		for _, hypothesis := range planned.Hypotheses {
			if strings.Contains(record.Claim, hypothesis.ID) && strings.Contains(record.Claim, "(was active)") {
				resolutionEvidence[hypothesis.ID]++
			}
		}
	}
	for _, hypothesis := range planned.Hypotheses {
		if resolutionEvidence[hypothesis.ID] != 1 {
			t.Fatalf("resolution evidence counts = %+v", resolutionEvidence)
		}
	}
	afterEvents, err := k1.Store().PhaseEvents(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterEvents, beforeEvents) {
		t.Fatalf("resolve appended phase events: before=%+v after=%+v", beforeEvents, afterEvents)
	}
}
