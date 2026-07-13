package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

func modelForSessionView(view kernel.SessionView) model {
	id := ""
	if view.Case != nil {
		id = view.Case.ID
	}
	return model{
		sessions: []kernel.SessionSummary{{ID: id, Slug: view.Slug}},
		detail:   detail{loaded: true, view: view}, width: 120, height: 40,
	}
}

func TestSessionDetailPrioritizesAssessmentDecisionAndNextAction(t *testing.T) {
	evidence := make([]domain.Evidence, 0, 6)
	for i := range 6 {
		evidence = append(evidence, domain.Evidence{
			Claim: fmt.Sprintf("evidence-%d", i), Confidence: domain.ConfidenceHigh,
		})
	}
	view := kernel.SessionView{
		Slug: "cortex",
		Case: &domain.CaseFile{
			ID: "task_pause", Goal: "choose a safe migration", Mode: domain.ModeChange, Risk: "medium",
			Status: domain.PhaseNeedsHumanDecision, PausedFrom: domain.PhaseInvestigating,
			Workspace: domain.Workspace{Repository: "cortex", CommitBefore: "abc123"},
		},
		Evidence: evidence,
		VerificationAssessment: kernel.VerificationAssessment{
			Outcome: kernel.VerificationPartial, MissingRequired: []string{"browser"},
			NonPassingClaims: []string{"migration preserves sessions"},
		},
		Decisions: []domain.Decision{{
			ID: "dec_1", Question: "Which migration should we ship?", Status: domain.DecisionPending,
			Options: []domain.DecisionOption{
				{ID: "safe", Label: "Two-step", Consequence: "slower, reversible rollout"},
				{ID: "fast", Label: "One-step", Consequence: "faster, harder rollback"},
			},
		}},
		Actions: []domain.NextAction{{
			Command: "cortex decision answer task_pause dec_1", Reason: "resume after a human choice",
			Inputs: []string{"answer", "responder"},
		}},
	}

	m := modelForSessionView(view)
	out := m.renderDetail(88)
	for _, want := range []string{
		"~ partial", "missing verifier: browser", "not passing: migration preserves sessions",
		"Decision needed", "Which migration should we ship?", "[safe]", "slower, reversible rollout",
		"Next", "cortex decision answer task_pause dec_1", "needs: answer, responder",
		"Recent Evidence", "(6 total)", "evidence-5", "… 1 older",
		"paused for human input", "an answer resumes investigating",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "evidence-0") {
		t.Errorf("detail should show recent evidence instead of the oldest entry:\n%s", out)
	}
}

func TestNeedsHumanDecisionStepperIsAResumablePause(t *testing.T) {
	out := loopStepper(domain.PhaseNeedsHumanDecision)
	if !strings.Contains(out, "paused · needs human decision") {
		t.Fatalf("decision wait should be presented as a pause: %s", out)
	}
	if strings.Contains(out, "■") {
		t.Fatalf("decision wait must not use the terminal-failure marker: %s", out)
	}
}

func TestSessionDetailMarksOnlyTheStaleReceiptID(t *testing.T) {
	view := kernel.SessionView{
		Slug:                   "cortex",
		Case:                   &domain.CaseFile{ID: "task_verify", Goal: "rerun proof", Status: domain.PhaseVerifying},
		VerificationAssessment: kernel.VerificationAssessment{Outcome: kernel.VerificationVerified},
		StaleVerification:      []string{"vr_old"},
		Receipts: []domain.VerificationRecord{
			{ID: "vr_old", Claim: "structural review", Surface: domain.SurfaceCode, Status: domain.VerifyPassed},
			{ID: "vr_fresh", Claim: "structural review", Surface: domain.SurfaceCode, Status: domain.VerifyPassed},
		},
	}
	m := modelForSessionView(view)
	if got := strings.Count(m.renderDetail(88), "(stale)"); got != 1 {
		t.Fatalf("same-claim fresh rerun was mislabeled; stale markers=%d", got)
	}
}

func TestSessionDetailUsesExactProjectionTotals(t *testing.T) {
	view := kernel.SessionView{
		Case:                   &domain.CaseFile{ID: "task_bounded", Goal: "inspect bounded history", Status: domain.PhaseVerifying},
		VerificationAssessment: kernel.VerificationAssessment{Outcome: kernel.VerificationPartial},
		Receipts: []domain.VerificationRecord{
			{ID: "vr_1", Claim: "claim one", Surface: domain.SurfaceCode, Status: domain.VerifyPassed},
			{ID: "vr_2", Claim: "claim two", Surface: domain.SurfaceCode, Status: domain.VerifyPassed},
			{ID: "vr_3", Claim: "claim three", Surface: domain.SurfaceCode, Status: domain.VerifyInconclusive},
			{ID: "vr_4", Claim: "claim four", Surface: domain.SurfaceCode, Status: domain.VerifyBlocked},
		},
		ReceiptTotal: 220,
		Evidence: []domain.Evidence{
			{Claim: "evidence one", Confidence: domain.ConfidenceHigh},
			{Claim: "evidence two", Confidence: domain.ConfidenceMedium},
			{Claim: "evidence three", Confidence: domain.ConfidenceLow},
			{Claim: "evidence four", Confidence: domain.ConfidenceHigh},
			{Claim: "evidence five", Confidence: domain.ConfidenceHigh},
		},
		EvidenceTotal:      250,
		ProjectionWarnings: []string{"showing 200 newest of 250 evidence records"},
	}
	m := modelForSessionView(view)
	out := m.renderDetail(88)
	for _, want := range []string{
		"(220 receipts)", "… 216 older receipts", "(250 total)", "… 245 older",
		"showing 200 newest of 250 evidence records",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail did not use canonical total %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(5 total)") {
		t.Fatalf("detail exposed retained evidence count as the total:\n%s", out)
	}
}
