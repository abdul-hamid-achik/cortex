package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// fromPlanCase starts a low-risk change case with one registered criterion,
// plans it with the given verification requirements, and edits the boundary
// file so verify has a real diff.
func fromPlanCase(t *testing.T, verifiers map[string]config.CommandVerifier, verification []string) (*Kernel, string) {
	t.Helper()
	t.Setenv("CORTEX_APPROVE_COMMANDS", "1")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
		Facts: []adapters.Fact{{Kind: "unit_test", Claim: "tests passed", Confidence: "high"}},
	}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = verifiers
	started, err := k.StartTask(context.Background(), StartInput{
		Goal: "average handles empty input", Risk: "low",
		AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "avg_ok", Statement: "average of an empty slice is zero"}},
	})
	if err != nil || !started.OK {
		t.Fatalf("start: %+v %v", started, err)
	}
	planned, err := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "average divides by zero", DisproveBy: "an empty-slice test passes before the fix"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   verification, Uncertainty: "other callers",
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan: %+v %v", planned, err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return k, started.TaskID
}

func criterionReceipt(t *testing.T, k *Kernel, taskID, claimID string) domain.VerificationRecord {
	t.Helper()
	receipts, err := k.Store().Verifications(taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range receipts {
		if receipt.ClaimID == claimID {
			return receipt
		}
	}
	t.Fatalf("no receipt for claim %q in %+v", claimID, receipts)
	return domain.VerificationRecord{}
}

func TestVerifyFromPlanNeverBindsCriteriaToStructuralReview(t *testing.T) {
	unit := map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	// codemap_review is listed first: the criterion must still bind to the
	// repository command, never to the structural diff review.
	k, taskID := fromPlanCase(t, unit, []string{"codemap_review", "command:unit"})
	verified, err := k.Verify(context.Background(), VerifyInput{TaskID: taskID, FromPlan: true})
	if err != nil || !verified.OK {
		t.Fatalf("from-plan verify: %+v %v", verified, err)
	}
	receipt := criterionReceipt(t, k, taskID, "avg_ok")
	if receipt.Contract != "unit" || receipt.Requirement == "codemap_review" || receipt.Tool == "codemap" {
		t.Fatalf("criterion bound to the wrong verifier: %+v", receipt)
	}
}

func TestVerifyFromPlanRefusesStructuralOnlyCriterionProof(t *testing.T) {
	k, taskID := fromPlanCase(t, nil, []string{"codemap_review"})
	rejected, err := k.Verify(context.Background(), VerifyInput{TaskID: taskID, FromPlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.OK {
		t.Fatalf("a structural review must not prove a behavioral criterion: %+v", rejected)
	}
	if !strings.Contains(rejected.Error, "structural") {
		t.Fatalf("error should explain the structural-review refusal: %s", rejected.Error)
	}
	var commands []string
	for _, action := range rejected.Actions {
		commands = append(commands, action.Command)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "init") || !strings.Contains(joined, "--claim-spec") {
		t.Fatalf("rejection should offer init and an explicit --claim-spec binding, got %q", joined)
	}
	receipts, _ := k.Store().Verifications(taskID)
	for _, receipt := range receipts {
		if receipt.ClaimID == "avg_ok" {
			t.Fatalf("a refused from-plan verify must not pin the criterion identity: %+v", receipt)
		}
	}
}

func TestVerifyFromPlanPrefersTheUnitTestCommand(t *testing.T) {
	verifiers := map[string]config.CommandVerifier{
		"build": {Argv: []string{"go", "build", "./..."}, Kind: domain.KindBuild, Surface: domain.SurfaceCode, Timeout: time.Minute},
		"lint":  {Argv: []string{"go", "vet", "./..."}, Kind: domain.KindLint, Surface: domain.SurfaceCode, Timeout: time.Minute},
		"unit":  {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	k, taskID := fromPlanCase(t, verifiers, []string{"command:build", "command:lint", "command:unit"})
	verified, err := k.Verify(context.Background(), VerifyInput{TaskID: taskID, FromPlan: true})
	if err != nil || !verified.OK {
		t.Fatalf("from-plan verify: %+v %v", verified, err)
	}
	if receipt := criterionReceipt(t, k, taskID, "avg_ok"); receipt.Contract != "unit" {
		t.Fatalf("criterion should bind to the unit-test command: %+v", receipt)
	}
}

func TestVerifyFromPlanRejectsAmbiguousCriterionBinding(t *testing.T) {
	verifiers := map[string]config.CommandVerifier{
		"fast": {Argv: []string{"go", "test", "-short", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
		"full": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	k, taskID := fromPlanCase(t, verifiers, []string{"command:fast", "command:full"})
	rejected, err := k.Verify(context.Background(), VerifyInput{TaskID: taskID, FromPlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.OK || !strings.Contains(rejected.Error, "command:fast") || !strings.Contains(rejected.Error, "command:full") {
		t.Fatalf("ambiguous binding should be refused naming the candidates: %+v", rejected)
	}
}

func TestCriterionWithoutVerdictCanRebindToARunnableVerifier(t *testing.T) {
	unit := map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	k, taskID := fromPlanCase(t, unit, []string{"command:unit"})
	statement := "average of an empty slice is zero"
	// The historical trap: the criterion was first bound to the structural
	// review, which never reached a verdict.
	structural, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "avg_ok", Statement: statement, Surface: domain.SurfaceCode, Verifier: "codemap", Contract: "codemap_review",
	}}})
	if !structural.OK {
		t.Fatalf("structural verify: %+v", structural)
	}
	if receipt := criterionReceipt(t, k, taskID, "avg_ok"); receipt.Status == domain.VerifyPassed || receipt.Status == domain.VerifyFailed {
		t.Fatalf("precondition: the structural binding should not reach a verdict here: %+v", receipt)
	}
	rebound, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "avg_ok", Statement: statement, Surface: domain.SurfaceCode, Verifier: "command:unit", Contract: "unit",
	}}})
	if !rebound.OK {
		t.Fatalf("a criterion that never reached a verdict should rebind: %+v", rebound)
	}
	status, _ := k.Status(context.Background(), taskID, "standard")
	if status.VerificationOutcome != VerificationVerified {
		t.Fatalf("rebound criterion should now satisfy the contract: %+v", status)
	}
	// Once the id has a verdict its binding is pinned again.
	moved, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "avg_ok", Statement: statement, Surface: domain.SurfaceCode, Verifier: "codemap", Contract: "codemap_review",
	}}})
	if moved.OK || !strings.Contains(moved.Error, "already reached a verdict") {
		t.Fatalf("a criterion with a verdict must keep its binding: %+v", moved)
	}
}

func TestStructuralReviewIsAdvisoryWhenARepositoryTestCommandExists(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "1")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
		Facts: []adapters.Fact{{Kind: "unit_test", Claim: "tests passed", Confidence: "high"}},
	}}
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusPartial}}
	k := newTestKernel(t, ws, command, codemap)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "fix the medium-risk callback", Risk: "medium"})
	planned, err := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a failing test reproduces it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "callers",
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan: %+v %v", planned, err)
	}
	c, _ := k.Store().Load(started.TaskID)
	for _, requirement := range c.VerificationRequired {
		if requirement == "codemap_review" {
			t.Fatalf("with a repository test command the structural review must be advisory, got %v", c.VerificationRequired)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !verified.OK {
		t.Fatalf("verify: %+v", verified)
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if status.VerificationOutcome != VerificationVerified {
		t.Fatalf("a passing repository test command should verify despite an inconclusive structural review: %+v", status)
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	reviewed := false
	for _, receipt := range receipts {
		if receipt.Requirement == "codemap_review" {
			reviewed = true
		}
	}
	if !reviewed {
		t.Fatal("the structural review should still run and leave an advisory receipt")
	}
}

func TestStructuralReviewStaysRequiredWithoutATestCommand(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "fix the medium-risk callback", Risk: "medium"})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a failing test reproduces it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "callers",
	})
	c, _ := k.Store().Load(started.TaskID)
	found := false
	for _, requirement := range c.VerificationRequired {
		found = found || requirement == "codemap_review"
	}
	if !found {
		t.Fatalf("without a test command the structural review remains the code proof: %v", c.VerificationRequired)
	}
}

func TestBlockedCommandVerifierNamesTheApprovalPath(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "fix the callback", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a failing test reproduces it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "callers",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if len(verified.Actions) == 0 || !strings.Contains(verified.Actions[0].Command, "setup --trust-commands") {
		t.Fatalf("a blocked test command should lead with the approval action: %+v", verified.Actions)
	}
	if !strings.Contains(strings.Join(verified.Warnings, "\n"), "CORTEX_APPROVE_COMMANDS") {
		t.Fatalf("the blocked warning should name the approval path: %v", verified.Warnings)
	}
}

func TestPlanRejectionsCarryARetryAction(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "fix the callback", Risk: "low"})
	noDisproof, _ := k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if noDisproof.OK || len(noDisproof.Actions) == 0 || !strings.Contains(noDisproof.Actions[0].Command, "--disprove DISPROOF") ||
		!strings.Contains(noDisproof.Error, "--disprove") {
		t.Fatalf("missing-disproof rejection should speak CLI and offer a retry: %+v", noDisproof)
	}
	noBoundary, _ := k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a test reproduces it"}}, Uncertainty: "u"})
	if noBoundary.OK || len(noBoundary.Actions) == 0 || !strings.Contains(noBoundary.Actions[0].Command, "--file FILE") {
		t.Fatalf("missing-boundary rejection should offer a retry with --file: %+v", noBoundary)
	}
	unknown, _ := k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a test reproduces it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Verification: []string{"command:unit"}, Uncertainty: "u"})
	if unknown.OK || len(unknown.Actions) < 2 || !strings.Contains(unknown.Actions[1].Command, " init") {
		t.Fatalf("unknown verifier rejection should list candidates and offer init: %+v", unknown)
	}
}

// A unit-test command only wins the implicit binding when every runnable
// candidate proves the code surface. With a terminal or browser flow also
// planned, the criterion might be about that behavior, so the choice is
// refused rather than handing it to go test.
func TestVerifyFromPlanRefusesCrossSurfaceGuess(t *testing.T) {
	unit := map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	k, taskID := fromPlanCase(t, unit, []string{"command:unit", "glyphrun_flow"})
	rejected, err := k.Verify(context.Background(), VerifyInput{TaskID: taskID, FromPlan: true, TerminalSpec: "specs/cli.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.OK || !strings.Contains(rejected.Error, "glyphrun") {
		t.Fatalf("a cross-surface plan must not bind criteria to the unit test implicitly: %+v", rejected)
	}
}
