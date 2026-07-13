/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

func TestRenderSessionViewShowsHumanDecisionAndUsefulContinuation(t *testing.T) {
	oldColor := useColor
	useColor = false
	t.Cleanup(func() { useColor = oldColor })

	evidence := make([]domain.Evidence, 0, 6)
	for i := range 6 {
		evidence = append(evidence, domain.Evidence{
			Claim: fmt.Sprintf("evidence-%d", i), Confidence: domain.ConfidenceMedium,
			Source: domain.Source{Tool: "codemap"},
		})
	}
	view := kernel.SessionView{
		Slug: "cortex",
		Case: &domain.CaseFile{
			ID: "task_pause", Goal: "choose a safe migration", Mode: domain.ModeChange, Risk: "medium",
			Status: domain.PhaseNeedsHumanDecision, PausedFrom: domain.PhaseInvestigating,
		},
		Evidence: evidence,
		VerificationAssessment: kernel.VerificationAssessment{
			Outcome: kernel.VerificationPartial, MissingRequired: []string{"browser"},
			FailedClaims: []string{"migration preserves sessions"},
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

	var out bytes.Buffer
	renderSessionViewTo(&out, view)
	text := out.String()
	for _, want := range []string{
		"⚠ [needs_human_decision]", "paused · needs human decision",
		"paused for human input · an answer resumes investigating",
		"Verification  ~ partial", "missing verifier: browser", "failed claim: migration preserves sessions",
		"Decision needed", "Which migration should we ship?", "[safe] Two-step — slower, reversible rollout",
		"Next", "cortex decision answer task_pause dec_1", "needs: answer, responder",
		"Recent Evidence  (6 total)", "… 1 older", "evidence-5", "codemap",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "■ needs_human_decision") || strings.Contains(text, "✗ [needs_human_decision]") {
		t.Errorf("human decision should not look terminal:\n%s", text)
	}
	if strings.Contains(text, "evidence-0") {
		t.Errorf("show should include recent evidence instead of the oldest entry:\n%s", text)
	}
}

func TestRenderSessionViewMarksOnlyTheStaleReceiptID(t *testing.T) {
	oldColor := useColor
	useColor = false
	t.Cleanup(func() { useColor = oldColor })

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
	var out bytes.Buffer
	renderSessionViewTo(&out, view)
	if got := strings.Count(out.String(), "(stale)"); got != 1 {
		t.Fatalf("same-claim fresh rerun was mislabeled; stale markers=%d:\n%s", got, out.String())
	}
}

func TestRenderSessionViewUsesExactProjectionTotals(t *testing.T) {
	oldColor := useColor
	useColor = false
	t.Cleanup(func() { useColor = oldColor })

	receipts := make([]domain.VerificationRecord, 5)
	evidence := make([]domain.Evidence, 5)
	timeline := make([]kernel.TimelineEntry, 8)
	for i := range receipts {
		receipts[i] = domain.VerificationRecord{Claim: fmt.Sprintf("receipt-%d", i), Status: domain.VerifyPassed}
		evidence[i] = domain.Evidence{Claim: fmt.Sprintf("evidence-%d", i), Confidence: domain.ConfidenceMedium}
	}
	for i := range timeline {
		timeline[i] = kernel.TimelineEntry{Summary: fmt.Sprintf("timeline-%d", i)}
	}
	view := kernel.SessionView{
		Case:                   &domain.CaseFile{ID: "task_totals", Goal: "show exact totals", Status: domain.PhaseVerifying},
		VerificationAssessment: kernel.VerificationAssessment{Outcome: kernel.VerificationPartial},
		Receipts:               receipts,
		ReceiptTotal:           220,
		Evidence:               evidence,
		EvidenceTotal:          250,
		Timeline:               timeline,
		TimelineTotal:          300,
	}
	var out bytes.Buffer
	renderSessionViewTo(&out, view)
	text := out.String()
	for _, want := range []string{
		"(220 receipts)", "… 215 older receipts",
		"Recent Evidence  (250 total)", "… 245 older",
		"Recent activity  (300 total)", "… 292 older",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show output missing exact total %q:\n%s", want, text)
		}
	}
}
