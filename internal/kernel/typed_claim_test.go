package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestTypedClaimSurfaceOverridesMisleadingWords(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "change login parser", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "parser needs a change", DisproveBy: "review rejects it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "coverage may be incomplete",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 9 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		Statement: "login UI words do not make this parser assertion a browser claim",
		Surface:   domain.SurfaceCode, Contract: "codemap_review",
	}}})
	if !result.OK {
		t.Fatalf("verify = %+v", result)
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	claims := latestReceipts(receipts, domain.VerificationPurposeNamedClaim)
	if len(claims) != 1 || claims[0].Surface != domain.SurfaceCode || claims[0].Tool != "codemap" || claims[0].Status != domain.VerifyPassed {
		t.Fatalf("typed claim was misrouted: %+v", claims)
	}
}

func TestTypedClaimsRequireContractsAndUniqueIDs(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "inspect", Mode: domain.ModeInvestigate})
	_, _ = k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	missing, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		Statement: "code is correct", Surface: domain.SurfaceCode,
	}}})
	if missing.OK {
		t.Fatalf("contractless typed claim accepted: %+v", missing)
	}
	duplicate, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{
		{ID: "claim_same", Statement: "first", Surface: domain.SurfaceCode, Contract: "codemap_review"},
		{ID: "claim_same", Statement: "second", Surface: domain.SurfaceCode, Contract: "codemap_review"},
	}})
	if duplicate.OK {
		t.Fatalf("duplicate claim ids accepted: %+v", duplicate)
	}
}

func TestTypedClaimContractMustMatchExecutedSpec(t *testing.T) {
	ws := testRepo(t)
	cairn := &fakeAdapter{name: "cairntrace", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
	}}
	k := newTestKernel(t, ws, cairn)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "change redirect", Risk: "low", Surfaces: []domain.Surface{domain.SurfaceBrowser},
	})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "redirect needs a change", DisproveBy: "browser flow fails"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Verification: []string{"cairntrace_flow"},
		Uncertainty: "other flows may differ",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 10 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, BrowserSpec: "specs/executed.yml",
		ClaimSpecs: []domain.VerificationClaim{{
			Statement: "checkout returns after login", Surface: domain.SurfaceBrowser,
			Verifier: "cairntrace", Contract: "specs/not-executed.yml",
		}},
	})
	receipts, _ := k.Store().Verifications(started.TaskID)
	claims := latestReceipts(receipts, domain.VerificationPurposeNamedClaim)
	if len(claims) != 1 || claims[0].Status != domain.VerifyNotRun || claims[0].Contract != "specs/not-executed.yml" {
		t.Fatalf("unmatched contract was treated as proof: %+v", claims)
	}
	assessment := assessVerification([]string{"cairntrace_flow"}, receipts)
	if assessment.Outcome != VerificationPartial {
		t.Fatalf("passing verifier plus unmatched required claim = %s, want partial", assessment.Outcome)
	}
}

func TestTypedClaimRejectsUnknownConfiguredVerifier(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "inspect behavior", Mode: domain.ModeInvestigate})
	_, _ = k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	result, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		Statement: "unit suite passes", Surface: domain.SurfaceCode, Verifier: "command:missing",
		Contract: "missing",
	}}})
	if result.OK || !strings.Contains(result.Error, "unknown configured verifier") {
		t.Fatalf("unknown command verifier accepted: %+v", result)
	}
}

func TestTypedRequiredClaimCannotDisappearFromSameRevisionRerun(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "preserve required claims", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 12 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "claim_keep", Statement: "the exact repair behavior is covered", Surface: domain.SurfaceCode,
		Contract: "a_contract_that_did_not_run",
	}}})
	if !first.OK {
		t.Fatalf("first verify = %+v", first)
	}
	second, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !second.OK {
		t.Fatalf("second verify = %+v", second)
	}
	if len(second.Actions) == 0 || second.Actions[0].Tool == "cortex_remember" {
		t.Fatalf("same-state rerun forgot the required claim in immediate actions: %+v", second.Actions)
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if status.VerificationOutcome != VerificationPartial {
		t.Fatalf("same-state rerun forgot the required claim: %+v", status)
	}
	renamed, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "claim_keep", Statement: "a different assertion reused the id", Surface: domain.SurfaceCode,
		Contract: "a_contract_that_did_not_run",
	}}})
	if renamed.OK || !strings.Contains(renamed.Error, "already identifies") {
		t.Fatalf("stable claim id was reused for a different assertion: %+v", renamed)
	}
}
