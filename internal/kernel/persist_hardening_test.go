package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestRememberFailsClosedOnCorruptCompletionState(t *testing.T) {
	for _, file := range []string{"verification.json", "evidence.jsonl", "hypotheses.json"} {
		t.Run(file, func(t *testing.T) {
			k, taskID := verifyingInvestigation(t, oneHypothesis("cause", "inspect the evidence"))
			leased, err := k.Store().Load(taskID)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			leased.ChangeLease = &domain.ChangeLease{
				Actor: "agent-a", AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(time.Hour),
			}
			if err := k.Store().Save(leased); err != nil {
				t.Fatal(err)
			}
			taskDir, err := k.Store().TaskDir(taskID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(taskDir, file), []byte("{corrupt\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := k.Store().Load(taskID)
			if err != nil {
				t.Fatal(err)
			}
			beforeEvents, err := k.Store().PhaseEvents(taskID)
			if err != nil {
				t.Fatal(err)
			}

			got, rememberErr := k.Remember(context.Background(), RememberInput{
				TaskID: taskID, Outcome: "done", VerificationNotPossible: true,
			})
			if rememberErr == nil || got.OK {
				t.Fatalf("remember succeeded with corrupt %s: %+v (%v)", file, got, rememberErr)
			}
			if !strings.Contains(got.Error, "cannot read completion state") {
				t.Fatalf("remember error does not identify completion state: %q", got.Error)
			}
			after, err := k.Store().Load(taskID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Status != domain.PhaseVerifying || after.Revision != before.Revision {
				t.Fatalf("corrupt state mutated case: before=%s/%d after=%s/%d", before.Status, before.Revision, after.Status, after.Revision)
			}
			if after.ChangeLease == nil || after.ChangeLease.ReleasedAt != nil || !after.ChangeLease.Active(time.Now().UTC()) {
				t.Fatalf("corrupt state released or removed the active lease: %+v", after.ChangeLease)
			}
			afterEvents, err := k.Store().PhaseEvents(taskID)
			if err != nil {
				t.Fatal(err)
			}
			if len(afterEvents) != len(beforeEvents) {
				t.Fatalf("corrupt state appended phase events: before=%d after=%d", len(beforeEvents), len(afterEvents))
			}
			if _, err := os.Stat(filepath.Join(taskDir, "summary.md")); !os.IsNotExist(err) {
				t.Fatalf("corrupt state wrote summary.md: %v", err)
			}
		})
	}
}

func TestRememberBoundsMaximumPlanAndLargeCompletionRecords(t *testing.T) {
	hyps := make([]HypothesisInput, 0, maxPlanHypotheses)
	for i := 0; i < maxPlanHypotheses; i++ {
		statementPrefix := fmt.Sprintf("hypothesis-%02d-", i)
		disproofPrefix := fmt.Sprintf("disproof-%02d-", i)
		hyps = append(hyps, HypothesisInput{
			Statement:  statementPrefix + strings.Repeat("h", maxRecordTextBytes-len(statementPrefix)),
			DisproveBy: disproofPrefix + strings.Repeat("d", maxRecordTextBytes-len(disproofPrefix)),
		})
	}
	k, taskID := verifyingInvestigation(t, hyps)

	const appendedEvidence = 240
	const sensitiveSentinel = "SENSITIVE-EVIDENCE-MUST-NOT-APPEAR"
	for i := 0; i < appendedEvidence; i++ {
		claim := fmt.Sprintf("evidence-%03d-%s", i, strings.Repeat("e", 8<<10))
		sensitivity := domain.SensitivityNormal
		if i%60 == 0 {
			claim = sensitiveSentinel + fmt.Sprintf("-%03d-%s", i, strings.Repeat("s", 8<<10))
			sensitivity = domain.SensitivitySensitive
		}
		if err := k.Store().AppendEvidence(taskID, domain.Evidence{
			ID: fmt.Sprintf("ev_large_%03d", i), Timestamp: time.Now().UTC(), Kind: domain.KindHumanReport,
			Source: domain.Source{Origin: "test"}, Claim: claim, Confidence: domain.ConfidenceMedium,
			Sensitivity: sensitivity,
		}); err != nil {
			t.Fatal(err)
		}
	}

	receipts := make([]domain.VerificationRecord, 0, maxCompletionSummaryReceipts+20)
	for i := 0; i < cap(receipts); i++ {
		prefix := fmt.Sprintf("receipt-%03d-", i)
		receipts = append(receipts, domain.VerificationRecord{
			ID: fmt.Sprintf("vr_large_%03d", i), BatchID: "vb_large_completion",
			Claim:   prefix + strings.Repeat("r", maxRecordTextBytes-len(prefix)),
			Surface: domain.SurfaceCode, Purpose: domain.VerificationPurposeVerifierRun,
			Tool: "codemap", Status: domain.VerifyInconclusive, Timestamp: time.Now().UTC(),
		})
	}
	if err := k.Store().AppendVerificationBatch(taskID, receipts); err != nil {
		t.Fatal(err)
	}

	snapshot, err := k.Store().CompletionSnapshot(taskID, maxCompletionSummaryEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Evidence) != maxCompletionSummaryEvidence {
		t.Fatalf("completion snapshot retained %d evidence records, want %d", len(snapshot.Evidence), maxCompletionSummaryEvidence)
	}
	if snapshot.SensitiveEvidenceOmitted != 4 {
		t.Fatalf("sensitive evidence count = %d, want 4", snapshot.SensitiveEvidenceOmitted)
	}

	got, err := k.Remember(context.Background(), RememberInput{
		TaskID: taskID, Outcome: "completed a maximum-size valid plan", VerificationNotPossible: true,
	})
	if err != nil || !got.OK || got.Phase != domain.PhaseComplete {
		t.Fatalf("remember failed for bounded maximum plan: %+v (%v)", got, err)
	}
	data, err := os.ReadFile(filepath.Join(k.Store().Root(), taskID, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	summary := string(data)
	if len(data) > maxCompletionSummaryBytes {
		t.Fatalf("summary size = %d, limit = %d", len(data), maxCompletionSummaryBytes)
	}
	for _, want := range []string{
		fmt.Sprintf("## Hypotheses (%d total)", maxPlanHypotheses),
		fmt.Sprintf("## Verification (%d receipts total)", snapshot.VerificationTotal),
		fmt.Sprintf("## Evidence (%d records total)", snapshot.EvidenceTotal),
		fmt.Sprintf("%d older non-sensitive and %d sensitive records omitted", snapshot.ShareableEvidenceTotal-len(snapshot.Evidence), snapshot.SensitiveEvidenceOmitted),
		"older, stale, or sensitive receipts omitted",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q", want)
		}
	}
	if strings.Contains(summary, sensitiveSentinel) {
		t.Fatal("summary copied explicitly sensitive evidence")
	}
	if strings.Contains(summary, hyps[0].Statement) || !strings.Contains(summary, "…") {
		t.Fatal("summary did not clip maximum-size record content")
	}
}

func verifyingInvestigation(t *testing.T, hypotheses []HypothesisInput) (*Kernel, string) {
	t.Helper()
	k := newTestKernel(t, testRepo(t))
	started, err := k.StartTask(context.Background(), StartInput{
		Goal: "exercise completion persistence", Mode: domain.ModeInvestigate,
	})
	if err != nil || !started.OK {
		t.Fatalf("start failed: %+v (%v)", started, err)
	}
	planned, err := k.Plan(PlanInput{TaskID: started.TaskID, Hypotheses: hypotheses, Uncertainty: "bounded test uncertainty"})
	if err != nil || !planned.OK {
		t.Fatalf("plan failed: %+v (%v)", planned, err)
	}
	verified, err := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if err != nil || !verified.OK || verified.Phase != domain.PhaseVerifying {
		t.Fatalf("verify failed: %+v (%v)", verified, err)
	}
	return k, started.TaskID
}

func oneHypothesis(statement, disproof string) []HypothesisInput {
	return []HypothesisInput{{Statement: statement, DisproveBy: disproof}}
}
