package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestBuildHandoffUsesCanonicalProjectionAndStaysBounded(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "handoff-repo")
	k := kernelAt(t, ws)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "handoff this work"})
	if err != nil || !started.OK {
		t.Fatalf("start: %+v %v", started, err)
	}
	for i := 0; i < maxHandoffEvidence+5; i++ {
		_, _ = k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: "observation", Actor: "tester"})
	}
	h, err := BuildHandoff(started.TaskID, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if h.TaskID != started.TaskID || len(h.Evidence) != maxHandoffEvidence || len(h.Actions) == 0 {
		t.Fatalf("handoff = %+v", h)
	}
	markdown := RenderHandoffMarkdown(h)
	if !strings.Contains(markdown, started.TaskID) || !strings.Contains(markdown, "Next actions") {
		t.Fatalf("markdown missing essentials:\n%s", markdown)
	}
}

func TestHandoffCarriesCoordinationMetadataAndCurrentNamedClaims(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	lease := &domain.ChangeLease{
		Actor: "agent-a", AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
	}
	h := Handoff{
		TaskID: "task_child", Revision: 9, Actor: "agent-a", ParentTaskID: "task_parent",
		ChildTaskIDs: []string{"task_grandchild"}, ChangeLease: lease,
	}
	markdown := RenderHandoffMarkdown(h)
	for _, want := range []string{"Revision:** 9", "agent-a", "task_parent", "task_grandchild", "Change lease"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("handoff metadata missing %q:\n%s", want, markdown)
		}
	}

	state := func(record domain.VerificationRecord, batch string) domain.VerificationRecord {
		record.BatchID = batch
		record.Revision = "commit-a"
		record.DirtyDigest = "dirty-a"
		record.Binding = domain.VerificationBound
		return record
	}
	receipts := []domain.VerificationRecord{
		state(domain.VerificationRecord{
			ID: "vr_old_run", Claim: "old review", Surface: domain.SurfaceCode, Tool: "codemap",
			Purpose: domain.VerificationPurposeVerifierRun, Requirement: "codemap_review", Status: domain.VerifyPassed,
		}, "vb_old"),
		state(domain.VerificationRecord{
			ID: "vr_carried_claim", ClaimID: "claim_login", Claim: "login still fails", Surface: domain.SurfaceCode,
			Tool: "codemap", Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyFailed,
		}, "vb_old"),
		state(domain.VerificationRecord{
			ID: "vr_new_run", Claim: "new review", Surface: domain.SurfaceCode, Tool: "codemap",
			Purpose: domain.VerificationPurposeVerifierRun, Requirement: "codemap_review", Status: domain.VerifyPassed,
		}, "vb_new"),
	}
	projected := latestHandoffReceipts(receipts)
	if len(projected) != 2 || projected[0].ID != "vr_new_run" || projected[1].ID != "vr_carried_claim" {
		t.Fatalf("handoff dropped current carried claim or retained old run: %+v", projected)
	}
}

func TestBuildHandoffProjectsDurableCoordinationState(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	workspace := repoNamed(t, "handoff-coordination")
	k := kernelAt(t, workspace)
	parent, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "parent work"}})
	child, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "owned child work", ParentTaskID: parent.TaskID, Actor: "agent-a",
	}})
	grandchild, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "nested check", ParentTaskID: child.TaskID, Actor: "agent-b",
	}})
	planned, err := k.Plan(PlanInput{
		TaskID: child.TaskID, Uncertainty: "runtime behavior remains uncertain",
		Hypotheses:     []HypothesisInput{{Statement: "callback needs repair", DisproveBy: "inspect the callback diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"f.go"}},
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan: %+v (%v)", planned, err)
	}
	begun, err := k.BeginChange(BeginChangeInput{TaskID: child.TaskID, Actor: "agent-a", TTL: 2 * time.Minute})
	if err != nil || !begun.OK {
		t.Fatalf("begin change: %+v (%v)", begun, err)
	}

	handoff, err := BuildHandoffIn(workspace, child.TaskID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Revision == 0 || handoff.Actor != "agent-a" || handoff.ParentTaskID != parent.TaskID {
		t.Fatalf("handoff coordination metadata = %+v", handoff)
	}
	if len(handoff.ChildTaskIDs) != 1 || handoff.ChildTaskIDs[0] != grandchild.TaskID {
		t.Fatalf("handoff child linkage = %v, want %s", handoff.ChildTaskIDs, grandchild.TaskID)
	}
	if handoff.ChangeLease == nil || handoff.ChangeLease.Actor != "agent-a" || handoff.ChangeLease.ExpiresAt.IsZero() {
		t.Fatalf("handoff lease = %+v", handoff.ChangeLease)
	}
}

func TestRenderHandoffMarkdownIncludesTransferCriticalFields(t *testing.T) {
	h := Handoff{
		TaskID: "task_handoff", Goal: "ship safely", Phase: domain.PhaseVerifying,
		Mode: domain.ModeChange, Risk: "high", Workspace: domain.Workspace{Repository: "cortex"},
		Boundary: domain.ChangeBoundary{Files: []string{"internal/kernel/verify.go"}},
		Plan: &domain.Plan{
			Uncertainty:          "browser coverage remains uncertain",
			VerificationRequired: []string{"codemap_review", "cairntrace_flow"},
		},
		Verification: VerificationAssessment{Outcome: VerificationPartial, MissingRequired: []string{"cairntrace_flow"}},
		Receipts: []domain.VerificationRecord{{
			ID: "vr_current", Claim: "structural review", Surface: domain.SurfaceCode,
			Tool: "codemap", Requirement: "codemap_review", Status: domain.VerifyPassed, Binding: domain.VerificationBound,
		}},
		Warnings: []string{"one historical receipt is stale"},
		Actions:  []domain.NextAction{{Tool: "cortex_verify", Command: "cortex verify task_handoff", Inputs: []string{"actor"}}},
	}

	markdown := RenderHandoffMarkdown(h)
	for _, want := range []string{
		"## Plan", "browser coverage remains uncertain", "cairntrace_flow",
		"### Current receipts", "vr_current", "codemap_review", "## Warnings",
		"one historical receipt is stale", "needs: actor",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("Markdown handoff missing %q:\n%s", want, markdown)
		}
	}
}

func TestHandoffHardByteBudgetPreservesContinuation(t *testing.T) {
	huge := strings.Repeat("界", maxHandoffBytes/3)
	h := Handoff{
		SchemaVersion: 1, TaskID: "task_large", Revision: 41, Goal: huge,
		Phase: domain.PhaseNeedsHumanDecision, Mode: domain.ModeChange,
		Workspace: domain.Workspace{Root: "/tmp/repository with spaces", Repository: "large-repo"},
		Verification: VerificationAssessment{
			Outcome:         VerificationPartial,
			MissingRequired: []string{huge}, NonPassingClaims: []string{huge},
		},
		Decisions: []domain.Decision{{
			ID: "dec_pending", Question: huge, Requester: "agent-a", Status: domain.DecisionPending,
			Options: []domain.DecisionOption{
				{ID: "small", Label: huge, Consequence: huge},
				{ID: "large", Label: huge, Consequence: huge},
			},
		}},
		Actions: []domain.NextAction{{
			Tool: "cortex_answer_decision", Command: "cortex decision answer task_large dec_pending",
			Arguments: map[string]any{"taskId": "task_large", "workspace": "/tmp/repository with spaces", "payload": huge},
		}},
	}
	for i := 0; i < maxHandoffEvidence+10; i++ {
		h.Evidence = append(h.Evidence, domain.FactView{ID: fmt.Sprintf("ev_%d", i), Claim: huge, Source: "test"})
		h.Receipts = append(h.Receipts, domain.VerificationRecord{ID: fmt.Sprintf("vr_%d", i), Claim: huge, Notes: huge})
		h.Hypotheses = append(h.Hypotheses, domain.Hypothesis{ID: fmt.Sprintf("hyp_%d", i), Statement: huge})
		h.Boundary.Files = append(h.Boundary.Files, huge)
	}

	bounded := boundHandoff(h)
	encoded, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxHandoffBytes {
		t.Fatalf("bounded handoff is %d bytes, limit %d", len(encoded), maxHandoffBytes)
	}
	if bounded.TaskID != h.TaskID || bounded.Revision != h.Revision || len(bounded.Actions) == 0 {
		t.Fatalf("continuation identity lost: %+v", bounded)
	}
	foundPending := false
	for _, decision := range bounded.Decisions {
		foundPending = foundPending || decision.ID == "dec_pending"
	}
	if !foundPending {
		t.Fatal("current pending decision was discarded by byte bounding")
	}
	if !strings.Contains(strings.Join(bounded.Warnings, " "), "bounded") {
		t.Fatalf("bounded packet has no explicit warning: %v", bounded.Warnings)
	}
	if h.Goal != huge || len(h.Evidence) != maxHandoffEvidence+10 {
		t.Fatal("bounding mutated the caller's canonical projection")
	}
	markdown := RenderHandoffMarkdown(h)
	if len(markdown) > maxHandoffBytes*2 || !strings.Contains(markdown, "dec_pending") {
		t.Fatalf("markdown transfer is not bounded or lost continuation: %d bytes", len(markdown))
	}
}

func TestBuildHandoffOmitsSensitiveRecordsButKeepsDecisionIdentity(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	workspace := repoNamed(t, "handoff-sensitive")
	k := kernelAt(t, workspace)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "transfer safely"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = k.RecordObservation(ObservationInput{
		TaskID: started.TaskID, Claim: "ordinary context", Actor: "reviewer", Sensitive: false,
	})
	_, _ = k.RecordObservation(ObservationInput{
		TaskID: started.TaskID, Claim: "private customer context", Actor: "reviewer", Sensitive: true,
	})
	if err := k.Store().AppendVerification(started.TaskID, domain.VerificationRecord{
		ID: "vr_private", Claim: "private verifier note", Status: domain.VerifyInconclusive,
		Purpose: domain.VerificationPurposeVerifierRun, Surface: domain.SurfaceCode,
		Sensitive: true, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	decision := domain.Decision{
		ID: "dec_private", Question: "private customer choice", Requester: "agent-a",
		RequestedAt: time.Now().UTC(), Status: domain.DecisionPending, Sensitive: true,
		Options: []domain.DecisionOption{
			{ID: "a", Label: "private A", Consequence: "private consequence A"},
			{ID: "b", Label: "private B", Consequence: "private consequence B"},
		},
	}
	if err := k.Store().AppendDecision(started.TaskID, decision); err != nil {
		t.Fatal(err)
	}

	handoff, err := BuildHandoffIn(workspace, started.TaskID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(handoff)
	text := string(encoded)
	for _, secret := range []string{"private customer context", "private verifier note", "private customer choice", "private consequence"} {
		if strings.Contains(text, secret) {
			t.Fatalf("sensitive content %q leaked in handoff: %s", secret, text)
		}
	}
	if !strings.Contains(text, "ordinary context") || !strings.Contains(text, "dec_private") {
		t.Fatalf("shareable evidence or pending decision identity lost: %s", text)
	}
	if !strings.Contains(strings.Join(handoff.Warnings, " "), "sensitive records omitted") {
		t.Fatalf("omission was silent: %v", handoff.Warnings)
	}
	for _, action := range handoff.Actions {
		if action.Tool == "cortex_request_decision" {
			if _, leaked := action.Arguments["question"]; leaked || len(action.BlockedBy) == 0 {
				t.Fatalf("sensitive decision repair action leaked or was not blocked: %+v", action)
			}
		}
	}
}

func TestCompletionHandoffPreservesSixtyFourClaimMultiBatchProofClosure(t *testing.T) {
	ledger := completionProofLedger(4, 16, func(batch, claim int) string {
		return fmt.Sprintf("criterion %02d-%02d is satisfied", batch, claim)
	})
	projection := projectHandoffReceipts(domain.PhaseComplete, VerificationVerified, ledger)
	if projection.sensitiveOmitted != 0 || len(projection.warnings) != 0 {
		t.Fatalf("unexpected proof projection omissions: %+v", projection)
	}
	if got, want := len(projection.receipts), 4*2+64; got != want {
		t.Fatalf("proof closure retained %d receipts, want %d", got, want)
	}
	assertEveryClaimHasVerifierBatch(t, projection.receipts)

	handoff := Handoff{
		SchemaVersion: 1, TaskID: "task_local_agent", Revision: 77,
		Goal:  strings.Repeat("large transfer context ", 8_000),
		Phase: domain.PhaseComplete, Mode: domain.ModeChange, Risk: "medium",
		Workspace:    domain.Workspace{Root: "/tmp/local-agent", Repository: "local-agent", Branch: "main"},
		Verification: VerificationAssessment{Outcome: VerificationVerified, SatisfiedRequired: []string{strings.Repeat("requirement", 2_000)}},
		Receipts:     projection.receipts,
		Evidence:     []domain.FactView{{ID: "ev_large", Claim: strings.Repeat("evidence", 20_000)}},
	}
	bounded := boundHandoff(handoff)
	if len(bounded.Receipts) != len(projection.receipts) {
		t.Fatalf("completion bounding split proof closure: retained %d of %d receipts", len(bounded.Receipts), len(projection.receipts))
	}
	assertEveryClaimHasVerifierBatch(t, bounded.Receipts)
	if bounded.Plan != nil || len(bounded.Evidence) != 0 || bounded.Goal != "" {
		t.Fatalf("non-proof transfer detail survived before proof was preserved: %+v", bounded)
	}
	if !strings.Contains(strings.Join(bounded.Warnings, " "), "local-agent result budget") {
		t.Fatalf("completion projection did not disclose bounding: %v", bounded.Warnings)
	}
	primary, err := handoffPrimaryJSON(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(primary) > maxCompletionHandoffBytes {
		t.Fatalf("pretty primary JSON is %d bytes, limit %d", len(primary), maxCompletionHandoffBytes)
	}
	for _, receipt := range bounded.Receipts {
		if receipt.EffectivePurpose() == domain.VerificationPurposeNamedClaim && !strings.HasSuffix(receipt.Claim, "is satisfied") {
			t.Fatalf("named claim was clipped: %q", receipt.Claim)
		}
	}
	if len(handoff.Receipts) != 72 || handoff.Goal == "" || len(handoff.Evidence) == 0 {
		t.Fatal("completion bounding mutated the caller's projection")
	}
}

func TestCompletionHandoffOmitsOversizeProofClosureAtomically(t *testing.T) {
	ledger := completionProofLedger(2, 32, func(batch, claim int) string {
		return fmt.Sprintf("criterion-%d-%d-%s", batch, claim, strings.Repeat("界", 1_350))
	})
	projection := projectHandoffReceipts(domain.PhaseComplete, VerificationVerified, ledger)
	if len(projection.receipts) != 68 {
		t.Fatalf("unbounded proof closure = %d receipts, want 68", len(projection.receipts))
	}

	bounded := boundHandoff(Handoff{
		SchemaVersion: 1, TaskID: "task_oversize_proof", Revision: 19,
		Phase: domain.PhaseComplete, Mode: domain.ModeChange,
		Workspace:    domain.Workspace{Repository: "local-agent"},
		Verification: VerificationAssessment{Outcome: VerificationVerified},
		Receipts:     projection.receipts,
	})
	if len(bounded.Receipts) != 0 {
		t.Fatalf("oversize proof was partially retained: %d receipts", len(bounded.Receipts))
	}
	if !strings.Contains(strings.Join(bounded.Warnings, " "), "proof receipts omitted atomically") {
		t.Fatalf("atomic proof omission was not explicit: %v", bounded.Warnings)
	}
	primary, err := handoffPrimaryJSON(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(primary) > maxCompletionHandoffBytes {
		t.Fatalf("overflow fallback primary JSON is %d bytes, limit %d", len(primary), maxCompletionHandoffBytes)
	}
	if len(projection.receipts) != 68 {
		t.Fatal("atomic proof bounding mutated its input closure")
	}
}

func TestCompletionProofClosureOmitsSensitiveBatchesAndDependentClaims(t *testing.T) {
	ledger := []domain.VerificationRecord{
		proofRun("run_safe", "batch_safe", false),
		proofClaim("claim_safe", "batch_safe", "criterion_safe", "safe criterion", false),
		proofRun("run_private", "batch_private", true),
		proofClaim("claim_dependent", "batch_private", "criterion_dependent", "dependent criterion", false),
		proofRun("run_mixed", "batch_mixed", false),
		proofClaim("claim_mixed", "batch_mixed", "criterion_mixed", "mixed safe criterion", false),
		proofClaim("claim_secret", "batch_mixed", "criterion_secret", "private criterion text", true),
	}
	projection := projectHandoffReceipts(domain.PhaseComplete, VerificationVerified, ledger)
	if projection.sensitiveOmitted != 2 {
		t.Fatalf("sensitive receipt count = %d, want 2", projection.sensitiveOmitted)
	}
	if len(projection.receipts) != 4 {
		t.Fatalf("safe proof projection = %+v, want two closed batches", projection.receipts)
	}
	assertEveryClaimHasVerifierBatch(t, projection.receipts)
	encoded, err := json.Marshal(projection.receipts)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"run_private", "claim_dependent", "dependent criterion", "private criterion text"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sensitive or orphaned proof %q survived: %s", forbidden, text)
		}
	}
	if !strings.Contains(strings.Join(projection.warnings, " "), "dependent named-claim") {
		t.Fatalf("dependent proof omission was silent: %v", projection.warnings)
	}
}

func TestNonterminalHandoffRetainsGenericReceiptBounding(t *testing.T) {
	ledger := completionProofLedger(4, 16, func(batch, claim int) string {
		return fmt.Sprintf("criterion %d-%d", batch, claim)
	})
	projection := projectHandoffReceipts(domain.PhaseVerifying, VerificationVerified, ledger)
	if len(projection.receipts) != 66 {
		// Generic behavior retains the two latest-batch verifier runs plus all
		// same-state named claims; it does not expand older verifier batches.
		t.Fatalf("nonterminal receipt projection = %d, want 66", len(projection.receipts))
	}
	bounded := boundHandoff(Handoff{
		SchemaVersion: 1, TaskID: "task_nonterminal", Phase: domain.PhaseVerifying,
		Verification: VerificationAssessment{Outcome: VerificationVerified}, Receipts: projection.receipts,
	})
	if len(bounded.Receipts) != handoffBudgets[0].receipts {
		t.Fatalf("generic handoff retained %d receipts, want existing bound %d", len(bounded.Receipts), handoffBudgets[0].receipts)
	}
	if bounded.Receipts[0].ID != projection.receipts[len(projection.receipts)-handoffBudgets[0].receipts].ID {
		t.Fatal("generic handoff no longer keeps the newest bounded receipt window")
	}
}

func completionProofLedger(batchCount, claimsPerBatch int, claimText func(batch, claim int) string) []domain.VerificationRecord {
	receipts := make([]domain.VerificationRecord, 0, batchCount*(claimsPerBatch+2))
	for batch := 0; batch < batchCount; batch++ {
		batchID := fmt.Sprintf("batch_%02d", batch)
		receipts = append(receipts,
			proofRun(fmt.Sprintf("run_%02d_code", batch), batchID, false),
			proofRun(fmt.Sprintf("run_%02d_terminal", batch), batchID, false),
		)
		for claim := 0; claim < claimsPerBatch; claim++ {
			receipts = append(receipts, proofClaim(
				fmt.Sprintf("claim_%02d_%02d", batch, claim), batchID,
				fmt.Sprintf("criterion_%02d_%02d", batch, claim), claimText(batch, claim), false,
			))
		}
	}
	return receipts
}

func proofRun(id, batchID string, sensitive bool) domain.VerificationRecord {
	return domain.VerificationRecord{
		ID: id, BatchID: batchID, Claim: "verifier run " + id,
		Surface: domain.SurfaceCode, Purpose: domain.VerificationPurposeVerifierRun,
		Tool: "go-test", Status: domain.VerifyPassed, Evidence: []string{"ev_" + id},
		Sensitive: sensitive, Revision: "commit_current", DirtyDigest: "sha256:dirty_current",
		Binding: domain.VerificationBound, Timestamp: time.Unix(10, 0).UTC(),
	}
}

func proofClaim(id, batchID, claimID, claim string, sensitive bool) domain.VerificationRecord {
	return domain.VerificationRecord{
		ID: id, BatchID: batchID, ClaimID: claimID, Claim: claim,
		Surface: domain.SurfaceCode, Purpose: domain.VerificationPurposeNamedClaim,
		Status: domain.VerifyPassed, Sensitive: sensitive,
		Revision: "commit_current", DirtyDigest: "sha256:dirty_current",
		Binding: domain.VerificationBound, Timestamp: time.Unix(10, 0).UTC(),
	}
}

func assertEveryClaimHasVerifierBatch(t *testing.T, receipts []domain.VerificationRecord) {
	t.Helper()
	verifierBatches := make(map[string]bool)
	for _, receipt := range receipts {
		if receipt.EffectivePurpose() == domain.VerificationPurposeVerifierRun {
			verifierBatches[receipt.BatchID] = true
		}
	}
	for _, receipt := range receipts {
		if receipt.EffectivePurpose() == domain.VerificationPurposeNamedClaim && !verifierBatches[receipt.BatchID] {
			t.Fatalf("named claim %s retained without verifier batch %s", receipt.ID, receipt.BatchID)
		}
	}
}
