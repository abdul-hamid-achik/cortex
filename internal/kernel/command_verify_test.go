package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestConfiguredCommandVerifierParticipatesInCanonicalAssessment(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "1")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
		Facts: []adapters.Fact{{Kind: "unit_test", Claim: "tests passed", Confidence: "high"}},
	}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "change callback", Risk: "low"})
	planned, _ := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback needs a change", DisproveBy: "unit tests fail"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"unit"}, Uncertainty: "test coverage may be incomplete",
	})
	if !planned.OK {
		t.Fatalf("plan = %+v", planned)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !verified.OK {
		t.Fatalf("verify = %+v", verified)
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	assessment := assessVerification([]string{"command:unit"}, receipts)
	if assessment.Outcome != VerificationVerified {
		t.Fatalf("assessment = %+v, receipts=%+v", assessment, receipts)
	}
	found := false
	for _, req := range command.requests() {
		found = found || (req.Operation == "unit" && req.Str("dir") == ws)
	}
	if !found {
		t.Fatal("configured command verifier was not executed in the task workspace")
	}
}

func TestConfiguredCommandFailureCannotBeLaunderedByStructuralPass(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "1")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictFailed,
		Facts: []adapters.Fact{{Kind: "lint", Claim: "lint failed", Confidence: "high"}},
	}}
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, command, codemap)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"lint": {Argv: []string{"golangci-lint", "run"}, Kind: domain.KindLint, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "change callback", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback needs a change", DisproveBy: "lint fails"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"codemap_review", "command:lint"}, Uncertainty: "u",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	receipts, _ := k.Store().Verifications(started.TaskID)
	assessment := assessVerification([]string{"codemap_review", "command:lint"}, receipts)
	if assessment.Outcome != VerificationFailed {
		t.Fatalf("failed command was laundered: %+v", assessment)
	}
}

func TestConfiguredCommandVerifierRequiresOutOfBandApproval(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
	}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "do not trust repository argv", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "tests should pass", DisproveBy: "unit verifier fails"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"command:unit"}, Uncertainty: "repository configuration is untrusted",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !verified.OK {
		t.Fatalf("blocked verifier should produce an honest receipt, not crash verify: %+v", verified)
	}
	if len(command.requests()) != 0 {
		t.Fatal("repository-configured argv executed without trusted approval")
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	foundBlocked := false
	for _, receipt := range receipts {
		if receipt.Requirement == "command:unit" && receipt.Status == domain.VerifyBlocked {
			foundBlocked = true
		}
	}
	if !foundBlocked {
		t.Fatalf("blocked configured execution was not recorded honestly: %+v", receipts)
	}
}
