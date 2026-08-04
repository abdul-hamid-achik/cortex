package kernel

import (
	"context"
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// This file covers the structured-continuation behavior added to rejection
// envelopes: instead of bare prose, a rejection now offers a retryable
// same-tool NextAction that pre-fills every value the request already
// supplied and names what's still missing, with Candidates populated only
// from real stored state or a fixed, already-documented vocabulary.

func TestResolveMissingReasonOffersHypothesisEvidenceAsCandidates(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "returnTo dropped", DisproveBy: "run browser flow"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u"})
	hypID := plan.Hypotheses[0].ID

	evs, err := k.Store().Evidence(id)
	if err != nil || len(evs) == 0 {
		t.Fatalf("expected orientation evidence: %v (%d records)", err, len(evs))
	}
	// Tie a real evidence record to the hypothesis directly (bypassing Resolve,
	// which requires a reason) so the candidate-reason path has something real
	// to surface.
	hyps, err := k.Store().Hypotheses(id)
	if err != nil {
		t.Fatal(err)
	}
	hyps[0].Supports = []string{evs[0].ID}
	if err := k.Store().SaveHypotheses(id, hyps); err != nil {
		t.Fatal(err)
	}

	res, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "rejected"})
	if res.OK {
		t.Fatal("missing reason should be rejected")
	}
	// Prose must not regress.
	if res.Error != "resolve needs a reason (what evidence changed the status)" {
		t.Fatalf("unexpected error text: %q", res.Error)
	}
	action := findAction(res.Actions, "cortex_resolve")
	if action == nil {
		t.Fatalf("expected a cortex_resolve continuation, got actions=%+v", res.Actions)
	}
	if action.Arguments["taskId"] != id || action.Arguments["hypothesisId"] != hypID || action.Arguments["status"] != "rejected" {
		t.Fatalf("continuation did not pre-fill known values: %+v", action.Arguments)
	}
	if !reflect.DeepEqual(action.Inputs, []string{"reason"}) {
		t.Fatalf("continuation inputs = %v, want [reason]", action.Inputs)
	}
	candidates := action.Candidates["reason"]
	if len(candidates) != 1 || candidates[0] != evs[0].Claim {
		t.Fatalf("candidate reasons = %v, want [%q] (the hypothesis's real supporting evidence claim)", candidates, evs[0].Claim)
	}
}

func TestResolveCitingPhantomEvidenceEnumeratesRealIDs(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u"})
	hypID := plan.Hypotheses[0].ID

	evs, err := k.Store().Evidence(id)
	if err != nil || len(evs) == 0 {
		t.Fatalf("expected orientation evidence: %v (%d records)", err, len(evs))
	}

	res, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "confirmed", Reason: "r", Evidence: []string{"ev_phantom"}})
	if res.OK {
		t.Fatal("citing a phantom evidence id should be rejected")
	}
	if res.Error != "no evidence ev_phantom in this task to cite" {
		t.Fatalf("unexpected error text: %q", res.Error)
	}
	action := findAction(res.Actions, "cortex_resolve")
	if action == nil {
		t.Fatalf("expected a cortex_resolve continuation, got actions=%+v", res.Actions)
	}
	if !reflect.DeepEqual(action.Inputs, []string{"evidence"}) {
		t.Fatalf("continuation inputs = %v, want [evidence]", action.Inputs)
	}
	candidates := action.Candidates["evidence"]
	if len(candidates) == 0 {
		t.Fatal("expected the task's real evidence ids as candidates")
	}
	for _, cand := range candidates {
		if cand == "ev_phantom" {
			t.Fatal("candidates must never include the phantom id — only real, stored evidence")
		}
	}
	foundReal := false
	for _, cand := range candidates {
		if cand == evs[0].ID {
			foundReal = true
		}
	}
	if !foundReal {
		t.Fatalf("candidates %v did not include real evidence id %q", candidates, evs[0].ID)
	}
}

func TestPlanCitingPhantomSupportEnumeratesRealEvidenceIDs(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	evs, err := k.Store().Evidence(started.TaskID)
	if err != nil || len(evs) == 0 {
		t.Fatalf("expected orientation evidence: %v (%d records)", err, len(evs))
	}

	got, _ := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d", Supports: []string{"ev_missing"}}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u",
	})
	if got.OK {
		t.Fatal("plan citing a phantom support id should be rejected")
	}
	action := findAction(got.Actions, "cortex_plan")
	if action == nil {
		t.Fatalf("expected a cortex_plan continuation, got actions=%+v", got.Actions)
	}
	if !reflect.DeepEqual(action.Inputs, []string{"support"}) {
		t.Fatalf("continuation inputs = %v, want [support]", action.Inputs)
	}
	candidates := action.Candidates["support"]
	if len(candidates) == 0 {
		t.Fatal("expected the task's real evidence ids as candidates")
	}
	for _, cand := range candidates {
		if cand == "ev_missing" {
			t.Fatal("candidates must never include the phantom id — only real, stored evidence")
		}
	}
}

func TestOpenTaskInvalidModeOffersEnumCandidatesAndKnownFields(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	got, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "fix the thing", Mode: domain.Mode("mutate"), Risk: "high",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK {
		t.Fatal("invalid mode accepted")
	}
	action := findAction(got.Actions, "cortex_open_task")
	if action == nil {
		t.Fatalf("expected a cortex_open_task continuation, got actions=%+v", got.Actions)
	}
	if action.Arguments["goal"] != "fix the thing" || action.Arguments["risk"] != "high" {
		t.Fatalf("continuation dropped known-good fields: %+v", action.Arguments)
	}
	if _, stillEchoed := action.Arguments["mode"]; stillEchoed {
		t.Fatalf("continuation must not echo the invalid mode as a known value: %+v", action.Arguments)
	}
	if !reflect.DeepEqual(action.Inputs, []string{"mode"}) {
		t.Fatalf("continuation inputs = %v, want [mode]", action.Inputs)
	}
	want := []string{"change", "investigate", "review"}
	if !reflect.DeepEqual(action.Candidates["mode"], want) {
		t.Fatalf("mode candidates = %v, want %v", action.Candidates["mode"], want)
	}

	ids, err := k.Store().List()
	if err != nil || len(ids) != 0 {
		t.Fatalf("an invalid open_task must not create a case: ids=%v err=%v", ids, err)
	}
}

func TestStartTaskInvalidSurfacesOffersEnumCandidates(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	got, err := k.StartTask(context.Background(), StartInput{Goal: "g", Surfaces: []domain.Surface{"desktop"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK {
		t.Fatal("invalid surfaces accepted")
	}
	action := findAction(got.Actions, "cortex_start_task")
	if action == nil {
		t.Fatalf("expected a cortex_start_task continuation, got actions=%+v", got.Actions)
	}
	want := []string{"code", "browser", "terminal", "artifact", "secret"}
	if !reflect.DeepEqual(action.Candidates["surfaces"], want) {
		t.Fatalf("surfaces candidates = %v, want %v", action.Candidates["surfaces"], want)
	}
}

func TestAnswerDecisionMissingOptionOffersPendingOptionIDs(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	reqEnv, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "which path?", Requester: "agent-a",
		Options: []domain.DecisionOption{
			{ID: "path_a", Label: "path a", Consequence: "x"},
			{ID: "path_b", Label: "path b", Consequence: "y"},
		},
	})
	if !reqEnv.OK || len(reqEnv.Artifacts) == 0 {
		t.Fatalf("request decision failed: %+v", reqEnv)
	}
	decisionID := reqEnv.Artifacts[0].ID

	got, _ := k.AnswerDecision(AnswerDecisionInput{TaskID: started.TaskID, DecisionID: decisionID, Responder: "human-1"})
	if got.OK {
		t.Fatal("missing answer should be rejected")
	}
	action := findAction(got.Actions, "cortex_answer_decision")
	if action == nil {
		t.Fatalf("expected a cortex_answer_decision continuation, got actions=%+v", got.Actions)
	}
	if action.Arguments["decisionId"] != decisionID || action.Arguments["responder"] != "human-1" {
		t.Fatalf("continuation dropped known fields: %+v", action.Arguments)
	}
	if !reflect.DeepEqual(action.Inputs, []string{"answer"}) {
		t.Fatalf("continuation inputs = %v, want [answer]", action.Inputs)
	}
	want := []string{"path_a", "path_b"}
	if !reflect.DeepEqual(action.Candidates["answer"], want) {
		t.Fatalf("answer candidates = %v, want %v (the pending decision's real option ids)", action.Candidates["answer"], want)
	}
}

func TestAnswerDecisionMissingResponderPrefillsKnownAnswer(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	reqEnv, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "which path?", Requester: "agent-a",
		Options: []domain.DecisionOption{
			{ID: "path_a", Label: "path a", Consequence: "x"},
			{ID: "path_b", Label: "path b", Consequence: "y"},
		},
	})
	decisionID := reqEnv.Artifacts[0].ID

	got, _ := k.AnswerDecision(AnswerDecisionInput{TaskID: started.TaskID, DecisionID: decisionID, Answer: "path_a"})
	if got.OK {
		t.Fatal("missing responder should be rejected")
	}
	action := findAction(got.Actions, "cortex_answer_decision")
	if action == nil {
		t.Fatalf("expected a cortex_answer_decision continuation, got actions=%+v", got.Actions)
	}
	if action.Arguments["answer"] != "path_a" || action.Arguments["decisionId"] != decisionID {
		t.Fatalf("continuation dropped known fields: %+v", action.Arguments)
	}
	if !reflect.DeepEqual(action.Inputs, []string{"responder"}) {
		t.Fatalf("continuation inputs = %v, want [responder]", action.Inputs)
	}
}

func TestRememberUnverifiedRejectionPrefillsAcknowledgmentFlag(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Mode: domain.ModeInvestigate})
	_, _ = k.Plan(PlanInput{TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})

	got, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "done"})
	if got.OK {
		t.Fatal("unverified remember should be rejected")
	}
	action := findAction(got.Actions, "cortex_remember")
	if action == nil {
		t.Fatalf("expected a cortex_remember continuation, got actions=%+v", got.Actions)
	}
	if flag, ok := action.Arguments["verificationNotPossible"].(bool); !ok || !flag {
		t.Fatalf("continuation did not pre-fill verificationNotPossible=true: %+v", action.Arguments)
	}
	// The retry-safe path (cortex_verify, in case the caller would rather
	// produce real proof) should also be offered.
	if findAction(got.Actions, "cortex_verify") == nil {
		t.Errorf("expected a cortex_verify continuation alongside remember, got actions=%+v", got.Actions)
	}
}

// TestRememberFailedRejectionPrefillsAcceptFailedFlag covers an
// investigate-mode task (no bounded change lease/boundary involved) so the
// canonical next action is the general verify/remember pair rather than
// begin-change: a change-mode task with a failed verifier is nudged back to
// bounded change work first (structuredNextForCaseAt's own, correct
// policy) — the flag-injection helper is a no-op there by design, since
// "return to bounded change work" is the better continuation in that case.
func TestRememberFailedRejectionPrefillsAcceptFailedFlag(t *testing.T) {
	ws := testRepo(t)
	glyph := &fakeAdapter{name: "glyphrun", caps: []adapters.Capability{adapters.CapabilityTerminal},
		result: adapters.Result{Tool: "glyphrun", Operation: "run", Status: adapters.StatusAuthoritative,
			Verdict: adapters.VerdictFailed, Summary: "spec failed",
			Facts: []adapters.Fact{{Kind: "terminal_run", Claim: "flow failed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, glyph)
	env, _ := k.StartTask(context.Background(), StartInput{
		Goal: "g", Mode: domain.ModeInvestigate, Surfaces: []domain.Surface{domain.SurfaceTerminal},
	})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "glyph run"}}, Uncertainty: "u"})
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id, TerminalSpec: "specs/x.yml", Claims: []string{"cli works"}})

	got, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "ship despite fail"})
	if got.OK {
		t.Fatal("failed remember should be rejected without accept_failed")
	}
	action := findAction(got.Actions, "cortex_remember")
	if action == nil {
		t.Fatalf("expected a cortex_remember continuation, got actions=%+v", got.Actions)
	}
	if flag, ok := action.Arguments["acceptFailed"].(bool); !ok || !flag {
		t.Fatalf("continuation did not pre-fill acceptFailed=true: %+v", action.Arguments)
	}
}
