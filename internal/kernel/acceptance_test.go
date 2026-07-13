package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestStartPersistsNormalizedAcceptanceCriteria(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, err := k.StartTask(context.Background(), StartInput{
		Goal: "criteria", AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: " criterion_1 ", Statement: " tests pass "}},
	})
	if err != nil || !started.OK {
		t.Fatalf("start = %+v err=%v", started, err)
	}
	c, err := k.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.AcceptanceCriterion{ID: "criterion_1", Statement: "tests pass"}
	if len(c.AcceptanceCriteria) != 1 || c.AcceptanceCriteria[0] != want {
		t.Fatalf("criteria = %#v, want %#v", c.AcceptanceCriteria, want)
	}
}

func TestOpenAcceptanceCriteriaAreImmutableAcrossIdempotentRetries(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	criteria := []domain.AcceptanceCriterion{
		{ID: "criterion_1", Statement: "tests pass"},
		{ID: "criterion_2", Statement: "build passes"},
	}
	first, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "ship", IdempotencyKey: "goal_1", AcceptanceCriteria: criteria,
	}})
	if err != nil || !first.OK {
		t.Fatalf("first open = %+v err=%v", first, err)
	}
	retry, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "retry wording", IdempotencyKey: "goal_1",
		AcceptanceCriteria: []domain.AcceptanceCriterion{criteria[1], criteria[0]},
	}})
	if err != nil || !retry.OK || retry.TaskID != first.TaskID {
		t.Fatalf("exact retry = %+v err=%v", retry, err)
	}
	mismatch, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "retry wording", IdempotencyKey: "goal_1",
		AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "a different contract"}},
	}})
	if err != nil || mismatch.OK || mismatch.TaskID != first.TaskID || !strings.Contains(mismatch.Error, "different acceptance criteria") {
		t.Fatalf("mismatched retry = %+v err=%v", mismatch, err)
	}
	ids, listErr := k.Store().List()
	if listErr != nil || len(ids) != 1 {
		t.Fatalf("mismatched retry created a task: ids=%v err=%v", ids, listErr)
	}
}

func TestOpenWithoutKeyDoesNotResumeDifferentAcceptanceContract(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	first, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "same goal", AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "first"}},
	}})
	second, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: " same  goal ", AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "second"}},
	}})
	if !first.OK || !second.OK || first.TaskID == second.TaskID {
		t.Fatalf("different immutable contracts coalesced: first=%+v second=%+v", first, second)
	}
}

func TestVerifyEnforcesRegisteredClaimStatementAndAllowsUnregisteredIDs(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "verify criteria", Risk: "low",
		AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "tests pass"}},
	})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	if err := os.WriteFile(ws+"/src/callback.go", []byte("package src\nfunc HandleCallback(){ _ = 21 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mismatch, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "criterion_1", Statement: "different statement", Surface: domain.SurfaceCode, Contract: "codemap_review",
	}}})
	if mismatch.OK || !strings.Contains(mismatch.Error, "registered for a different") {
		t.Fatalf("registered mismatch accepted: %+v", mismatch)
	}
	unregistered, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: "extra_claim", Statement: "extra compatibility claim", Surface: domain.SurfaceCode, Contract: "codemap_review",
	}}})
	if !unregistered.OK {
		t.Fatalf("unregistered named claim rejected: %+v", unregistered)
	}
	status, err := k.Status(context.Background(), started.TaskID, "standard")
	if err != nil {
		t.Fatal(err)
	}
	if status.VerificationOutcome == VerificationVerified || len(status.MissingCriteria) != 1 || status.MissingCriteria[0] != "criterion_1" {
		t.Fatalf("missing registered criterion did not gate verification: %+v", status)
	}
	remembered, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "done"})
	if remembered.OK {
		t.Fatalf("remember ignored missing registered criterion: %+v", remembered)
	}
	acknowledged, _ := k.Remember(context.Background(), RememberInput{
		TaskID: started.TaskID, Outcome: "still done", VerificationNotPossible: true,
	})
	if acknowledged.OK || !strings.Contains(acknowledged.Error, "registered acceptance") {
		t.Fatalf("unverified acknowledgement bypassed immutable acceptance contract: %+v", acknowledged)
	}
}

func TestRegisteredCriterionProducesCanonicalStatusProofAndCompletes(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{
		Status: adapters.StatusAuthoritative,
		Facts:  []adapters.Fact{{Kind: "code_graph", Claim: "reviewed", Confidence: "high"}},
	}}
	k := newTestKernel(t, ws, codemap)
	criterion := domain.AcceptanceCriterion{ID: "criterion_1", Statement: "tests pass"}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "verify criteria", Risk: "low", AcceptanceCriteria: []domain.AcceptanceCriterion{criterion}})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	if err := os.WriteFile(ws+"/src/callback.go", []byte("package src\nfunc HandleCallback(){ _ = 22 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
		ID: criterion.ID, Statement: criterion.Statement, Surface: domain.SurfaceCode, Contract: "codemap_review",
	}}})
	if !verified.OK {
		t.Fatalf("verify = %+v", verified)
	}
	status, err := k.Status(context.Background(), started.TaskID, "standard")
	if err != nil {
		t.Fatal(err)
	}
	if status.VerificationOutcome != VerificationVerified || len(status.ClaimProofs) != 1 || status.ClaimProofTotal != 1 {
		t.Fatalf("status proof manifest = %+v", status)
	}
	proof := status.ClaimProofs[0]
	if proof.ClaimID != criterion.ID || proof.Statement != criterion.Statement || proof.StatementDigest == "" ||
		proof.Status != domain.VerifyPassed || proof.Binding != domain.VerificationBound || proof.BatchID == "" ||
		proof.Revision == "" || proof.DirtyDigest == "" || len(proof.Evidence) == 0 {
		t.Fatalf("claim proof = %#v", proof)
	}
	if !containsString(proof.Evidence, "case://"+started.TaskID+"/verification/"+proof.ReceiptID) {
		t.Fatalf("claim proof does not lead with its durable named receipt: %#v", proof)
	}
	remembered, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "done"})
	if !remembered.OK || remembered.Phase != domain.PhaseComplete {
		t.Fatalf("remember = %+v", remembered)
	}
}

func TestStatusProofManifestOmitsSensitiveRefsAndRetainsLegacyClaimIDs(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "legacy proofs"})
	c, _ := k.Store().Load(started.TaskID)
	c.Status = domain.PhaseComplete // terminal projections retain the completed proof state
	if err := k.Store().Save(c); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	receipts := []domain.VerificationRecord{
		{ID: "vr_run", BatchID: "vb_1", Claim: "structural review", Surface: domain.SurfaceCode, Tool: "codemap",
			Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyPassed, Binding: domain.VerificationBound,
			Evidence: []string{"ev_secret"}, Sensitive: true, Revision: "commit", DirtyDigest: "dirty", Timestamp: when},
		{ID: "vr_claim", BatchID: "vb_1", ClaimID: "legacy_claim", Claim: "legacy statement", Surface: domain.SurfaceCode, Tool: "codemap",
			Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyPassed, Binding: domain.VerificationBound,
			Revision: "commit", DirtyDigest: "dirty", Timestamp: when},
	}
	for _, receipt := range receipts {
		if err := k.Store().AppendVerification(started.TaskID, receipt); err != nil {
			t.Fatal(err)
		}
	}
	status, err := k.Status(context.Background(), started.TaskID, "standard")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.ClaimProofs) != 1 || status.ClaimProofs[0].ClaimID != "legacy_claim" || !status.ClaimProofs[0].SensitiveRefsOmitted {
		t.Fatalf("legacy proof = %#v", status.ClaimProofs)
	}
	for _, ref := range status.ClaimProofs[0].Evidence {
		if strings.Contains(ref, "secret") || strings.Contains(ref, "vr_run") {
			t.Fatalf("sensitive ref leaked: %q", ref)
		}
	}
}

func TestStatusWorstCaseAcceptanceProofManifestStaysBelowLocalAgentLimit(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	criteria := make([]domain.AcceptanceCriterion, domain.MaxAcceptanceCriteria)
	for i := range criteria {
		criteria[i] = domain.AcceptanceCriterion{
			ID:        "criterion_" + strings.Repeat("x", 110) + fmt.Sprintf("%02d", i),
			Statement: strings.Repeat("界", domain.MaxAcceptanceCriterionStatementBytes/len("界")),
		}
	}
	started, err := k.StartTask(context.Background(), StartInput{Goal: "bounded status", AcceptanceCriteria: criteria})
	if err != nil || !started.OK {
		t.Fatalf("start = %+v err=%v", started, err)
	}
	status, err := k.Status(context.Background(), started.TaskID, "standard")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 90<<10 {
		t.Fatalf("worst-case status = %d bytes, want safely below 90 KiB", len(encoded))
	}
	if len(status.ClaimProofs) != domain.MaxAcceptanceCriteria || status.ClaimProofTotal != domain.MaxAcceptanceCriteria {
		t.Fatalf("proof projection lost exact totals: len=%d total=%d", len(status.ClaimProofs), status.ClaimProofTotal)
	}
	for _, proof := range status.ClaimProofs {
		if !proof.StatementTruncated || proof.StatementDigest == "" || len(proof.Statement) > maxClaimProofStatementBytes {
			t.Fatalf("unbounded proof statement: %#v", proof)
		}
	}
}
