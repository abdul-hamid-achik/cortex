package kernel

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func decisionOptions(secret string) []domain.DecisionOption {
	return []domain.DecisionOption{
		{ID: "small", Label: "Make the small repair", Consequence: "Touches one file"},
		{ID: "broad", Label: "Make the broad repair", Consequence: "Requires reviewing " + secret},
	}
}

func TestDecisionPausesAndResumesExactPhase(t *testing.T) {
	const secret = "ghp_16C7e42F292c6912E7710c838347Ae178B4a99"
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	requested, err := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair should use " + secret + "?",
		Options: decisionOptions(secret), Requester: "agent",
	})
	if err != nil || !requested.OK || requested.Phase != domain.PhaseNeedsHumanDecision {
		t.Fatalf("request decision: ok=%v phase=%s err=%v envelope=%s", requested.OK, requested.Phase, err, requested.Error)
	}
	if len(requested.Artifacts) != 1 || requested.Artifacts[0].ID == "" {
		t.Fatalf("decision id not returned: %+v", requested.Artifacts)
	}
	decisionID := requested.Artifacts[0].ID
	if len(requested.Actions) != 1 || requested.Actions[0].Tool != "cortex_answer_decision" || requested.Actions[0].Arguments["decisionId"] != decisionID {
		t.Fatalf("decision continuation is not executable: %+v", requested.Actions)
	}
	c, _ := k.Store().Load(started.TaskID)
	if c.PausedFrom != domain.PhaseInvestigating || c.Status != domain.PhaseNeedsHumanDecision || c.Status.IsTerminal() {
		t.Fatalf("case not paused correctly: status=%s pausedFrom=%s terminal=%t", c.Status, c.PausedFrom, c.Status.IsTerminal())
	}
	status, _ := k.Status(context.Background(), c.ID, "standard")
	if status.PendingDecision == nil || status.PendingDecision.ID != decisionID || len(status.Actions) != 1 ||
		status.Actions[0].Arguments["decisionId"] != decisionID || strings.Contains(status.Actions[0].Command, "<decisionId>") {
		t.Fatalf("status did not bind pending decision continuation: %+v", status)
	}
	decision, _ := k.Store().Decision(c.ID, decisionID)
	if strings.Contains(decision.Question, secret) || strings.Contains(decision.Options[1].Consequence, secret) || !decision.Sensitive {
		t.Fatalf("decision was not redacted/labeled: %+v", decision)
	}

	// Ordinary lifecycle operations stay gated while the task waits.
	if got, _ := k.Investigate(context.Background(), InvestigateInput{TaskID: c.ID, Question: "continue"}); got.OK {
		t.Error("investigate should not run while waiting for a decision")
	}
	if got, _ := k.Plan(PlanInput{TaskID: c.ID}); got.OK {
		t.Error("plan should not run while waiting for a decision")
	}
	if got, _ := k.ResumeDecision(c.ID); got.OK {
		t.Error("resume must not bypass a pending answer")
	}
	if got, _ := k.AnswerDecision(AnswerDecisionInput{TaskID: c.ID, DecisionID: decisionID, Answer: "missing", Responder: "human"}); got.OK {
		t.Error("answer outside the option set should fail")
	}

	answered, err := k.AnswerDecision(AnswerDecisionInput{
		TaskID: c.ID, DecisionID: decisionID, Answer: "small", Responder: "human " + secret,
	})
	if err != nil || !answered.OK || answered.Phase != domain.PhaseInvestigating {
		t.Fatalf("answer decision: ok=%v phase=%s err=%v envelope=%s", answered.OK, answered.Phase, err, answered.Error)
	}
	c, _ = k.Store().Load(c.ID)
	if c.PausedFrom != "" || c.Status != domain.PhaseInvestigating {
		t.Fatalf("case did not resume exactly: status=%s pausedFrom=%s", c.Status, c.PausedFrom)
	}
	decision, _ = k.Store().Decision(c.ID, decisionID)
	if decision.Status != domain.DecisionAnswered || decision.Answer != "small" || decision.AnsweredAt == nil || decision.EvidenceID == "" {
		t.Fatalf("answer fields missing: %+v", decision)
	}
	if strings.Contains(decision.Responder, secret) {
		t.Fatalf("decision responder leaked secret: %+v", decision)
	}
	ev, err := k.Store().GetEvidence(c.ID, decision.EvidenceID)
	if err != nil {
		t.Fatalf("decision evidence missing: %v", err)
	}
	if ev.Kind != domain.KindHumanReport || ev.Category != "decision" || ev.Source.Origin != "human" || strings.Contains(ev.Claim, secret) || ev.Sensitivity != domain.SensitivitySensitive {
		t.Fatalf("decision evidence is not redacted human_report: %+v", ev)
	}
}

func TestDecisionResumesPlannedAndBlocksResolutionWhileWaiting(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	planned, _ := k.Plan(PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "h", DisproveBy: "d",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	requested, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	if !requested.OK {
		t.Fatalf("request rejected: %s", requested.Error)
	}
	if got, _ := k.Resolve(ResolveInput{
		TaskID: started.TaskID, HypothesisID: planned.Hypotheses[0].ID,
		Status: "rejected", Reason: "changed our mind",
	}); got.OK {
		t.Error("hypothesis resolution should be gated while waiting")
	}
	decisionID := requested.Artifacts[0].ID
	answered, _ := k.AnswerDecision(AnswerDecisionInput{
		TaskID: started.TaskID, DecisionID: decisionID, Answer: "broad", Responder: "reviewer",
	})
	if !answered.OK || answered.Phase != domain.PhasePlanned {
		t.Fatalf("planned task resumed to %s: %s", answered.Phase, answered.Error)
	}
}

func TestResumeDecisionRecoversAnsweredWait(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	requested, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	decisionID := requested.Artifacts[0].ID
	// Simulate a process stopping after decisions.json was updated but before the
	// answer evidence and resumed case.json were written.
	if _, err := k.Store().AnswerDecision(
		started.TaskID, decisionID, "small", "human", "ev_recovery", time.Now().UTC(), false,
	); err != nil {
		t.Fatal(err)
	}
	resumed, err := k.ResumeDecision(started.TaskID)
	if err != nil || !resumed.OK || resumed.Phase != domain.PhaseInvestigating {
		t.Fatalf("resume recovery: ok=%v phase=%s err=%v envelope=%s", resumed.OK, resumed.Phase, err, resumed.Error)
	}
	if _, err := k.Store().GetEvidence(started.TaskID, "ev_recovery"); err != nil {
		t.Fatalf("recovery did not backfill decision evidence: %v", err)
	}
}

func TestRequestDecisionRecoversPendingRecordBeforeCasePause(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	orphan, err := k.buildDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process stopping after decisions.json landed but before the
	// case snapshot moved into needs_human_decision.
	if err := k.Store().AppendDecision(started.TaskID, orphan); err != nil {
		t.Fatal(err)
	}
	recovered, err := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "a retry may not know the original question",
		Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	if err != nil || !recovered.OK || recovered.Phase != domain.PhaseNeedsHumanDecision {
		t.Fatalf("recover orphan request: err=%v envelope=%+v", err, recovered)
	}
	if len(recovered.Artifacts) != 1 || recovered.Artifacts[0].ID != orphan.ID {
		t.Fatalf("retry created or returned the wrong decision: %+v", recovered.Artifacts)
	}

	// Response loss after the successful pause is idempotent too.
	retry, err := k.RequestDecision(RequestDecisionInput{TaskID: started.TaskID})
	if err != nil || !retry.OK || retry.Artifacts[0].ID != orphan.ID {
		t.Fatalf("paused retry: err=%v envelope=%+v", err, retry)
	}
	events, _ := k.Store().PhaseEvents(started.TaskID)
	pauses := 0
	for _, event := range events {
		if event.To == domain.PhaseNeedsHumanDecision {
			pauses++
		}
	}
	if pauses != 1 {
		t.Fatalf("decision pause history has %d entries, want 1: %+v", pauses, events)
	}
}

func TestConcurrentDecisionRequestsCoalesce(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	started, _ := k1.StartTask(context.Background(), StartInput{Goal: "g"})
	start := make(chan struct{})
	results := make(chan domain.Envelope, 2)
	var wg sync.WaitGroup
	for i, k := range []*Kernel{k1, k2} {
		wg.Add(1)
		go func(i int, k *Kernel) {
			defer wg.Done()
			<-start
			env, _ := k.RequestDecision(RequestDecisionInput{
				TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"),
				Requester: "agent",
			})
			results <- env
		}(i, k)
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if !result.OK || result.Phase != domain.PhaseNeedsHumanDecision {
			t.Fatalf("racing request did not coalesce: %+v", result)
		}
	}
	decisions, _ := k1.Store().Decisions(started.TaskID)
	if len(decisions) != 1 || decisions[0].Status != domain.DecisionPending {
		t.Fatalf("racing requests produced %+v", decisions)
	}
}

func TestAnswerDecisionRetryIsIdempotent(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	requested, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	input := AnswerDecisionInput{
		TaskID: started.TaskID, DecisionID: requested.Artifacts[0].ID, Answer: "small", Responder: "human",
	}
	first, err := k.AnswerDecision(input)
	if err != nil || !first.OK {
		t.Fatalf("first answer: err=%v envelope=%+v", err, first)
	}
	retry, err := k.AnswerDecision(input)
	if err != nil || !retry.OK || retry.Phase != domain.PhaseInvestigating || !strings.Contains(retry.Summary, "already answered") {
		t.Fatalf("answer retry: err=%v envelope=%+v", err, retry)
	}
	decision, _ := k.Store().Decision(started.TaskID, input.DecisionID)
	evidence, _ := k.Store().Evidence(started.TaskID)
	matches := 0
	for _, item := range evidence {
		if item.ID == decision.EvidenceID {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("answer retry produced %d copies of evidence %s", matches, decision.EvidenceID)
	}
	wrong, _ := k.AnswerDecision(AnswerDecisionInput{
		TaskID: started.TaskID, DecisionID: input.DecisionID, Answer: "broad", Responder: "human",
	})
	if wrong.OK {
		t.Fatalf("different replay answer was accepted: %+v", wrong)
	}
}

func TestConcurrentIdenticalDecisionAnswersAreIdempotent(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	started, _ := k1.StartTask(context.Background(), StartInput{Goal: "g"})
	requested, _ := k1.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	input := AnswerDecisionInput{
		TaskID: started.TaskID, DecisionID: requested.Artifacts[0].ID, Answer: "small", Responder: "human",
	}
	start := make(chan struct{})
	results := make(chan domain.Envelope, 2)
	var wg sync.WaitGroup
	for _, k := range []*Kernel{k1, k2} {
		wg.Add(1)
		go func(k *Kernel) {
			defer wg.Done()
			<-start
			env, _ := k.AnswerDecision(input)
			results <- env
		}(k)
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if !result.OK || result.Phase != domain.PhaseInvestigating {
			t.Fatalf("identical racing answer did not converge: %+v", result)
		}
	}
	decision, _ := k1.Store().Decision(started.TaskID, input.DecisionID)
	evidence, _ := k1.Store().Evidence(started.TaskID)
	matches := 0
	for _, item := range evidence {
		if item.ID == decision.EvidenceID {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("racing answer produced %d copies of evidence %s", matches, decision.EvidenceID)
	}
}

func TestRequestDecisionRejectsTerminalTask(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.AbortTask(started.TaskID, "stop")
	got, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "Which repair?", Options: decisionOptions("nothing-sensitive"), Requester: "agent",
	})
	if got.OK || !strings.Contains(got.Error, "terminal phase") {
		t.Fatalf("terminal task accepted decision request: ok=%v error=%q", got.OK, got.Error)
	}
}
