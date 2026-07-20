/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:     "show <taskId>",
	Aliases: []string{"view"},
	Short:   "Bounded read-only view of one session — loop, hypotheses, verification, time-in-phase, recent activity",
	Long: `Show the current state of a session in one screen: its place in the reasoning
loop, hypotheses, verification receipts, time spent in each phase, and recent
activity with exact ledger totals. Works from any directory — the session is located by ID across the
central store, so you can inspect a task from another repo without cd-ing there. This is the
recommended single-session view; for focused projections use cortex status (agent checkpoint),
cortex timeline (chronological feed), or cortex metrics (outcome metrics).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := kernel.ShowSessionIn(workspaceArg(cmd), args[0])
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(v)
		}
		renderSessionView(v)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
}

func renderSessionView(v kernel.SessionView) {
	renderSessionViewTo(os.Stdout, v)
}

func renderSessionViewTo(w io.Writer, v kernel.SessionView) {
	c := v.Case
	head := paint(styOK, "✓")
	phaseStyle := styPhase
	if c.Status == domain.PhaseNeedsHumanDecision {
		head = paint(styWarn, "⚠")
		phaseStyle = styWarn
	} else if c.Status.IsTerminal() && c.Status != domain.PhaseComplete {
		head = paint(styErr, "✗")
	}
	pln(w, head+" "+paint(phaseStyle, "["+string(c.Status)+"]")+" "+c.Goal)
	pln(w, paint(styMuted, "  "+c.ID+" · "+v.Slug+" · "+string(c.Mode)+" · risk "+c.Risk))
	pln(w, "  "+loopStepperLine(c.Status))
	if c.Status == domain.PhaseNeedsHumanDecision {
		pln(w, "  "+paint(styWarn, "⚠ paused for human input · an answer resumes "+string(c.PausedFrom)))
	}

	receiptTotal := maxInt(v.ReceiptTotal, len(v.Receipts))
	verificationHeading := heading("Verification") + "  " + verificationAssessmentLabel(v.VerificationAssessment)
	if receiptTotal > 0 {
		verificationHeading += paint(styMuted, fmt.Sprintf("  (%d receipts)", receiptTotal))
	}
	pln(w, verificationHeading)
	for _, gap := range verificationAssessmentGaps(v.VerificationAssessment) {
		pln(w, "  "+paint(styWarn, "⚠ "+clipLine(gap, 104)))
	}
	for _, warning := range v.VerificationWarnings {
		pln(w, "  "+paint(styWarn, "⚠ "+clipLine(warning, 104)))
	}
	if len(v.StaleVerification) > 0 {
		pln(w, "  "+paint(styWarn, fmt.Sprintf("⚠ %d receipt(s) are stale for the current HEAD/diff", len(v.StaleVerification))))
	}
	if len(v.Receipts) > 0 {
		start := maxInt(0, len(v.Receipts)-5)
		older := maxInt(0, receiptTotal-(len(v.Receipts)-start))
		if older > 0 {
			pln(w, paint(styMuted, fmt.Sprintf("  … %d older receipts", older)))
		}
		for _, r := range v.Receipts[start:] {
			mark, suffix := receiptMarkCLI(r.Status), ""
			staleKey := r.ID
			if staleKey == "" {
				staleKey = r.Claim
			}
			if stringIn(v.StaleVerification, staleKey) {
				mark, suffix = paint(styWarn, "!"), paint(styWarn, " (stale)")
			}
			pf(w, "  %s %s %s%s\n", mark, paint(styMuted, string(r.Surface)), clipLine(r.Claim, 78), suffix)
		}
	}

	if decision := pendingSessionDecision(v.Decisions); decision != nil {
		pln(w, heading("Decision needed"))
		pln(w, "  "+paint(styWarn, "? ")+clipLine(decision.Question, 100))
		for _, option := range decision.Options {
			pf(w, "  %s %s — %s\n", paint(styPhase, "["+option.ID+"]"), clipLine(option.Label, 28), clipLine(option.Consequence, 66))
		}
	}

	if len(v.Actions) > 0 {
		action := v.Actions[0]
		pln(w, heading("Next"))
		pln(w, "  "+paint(styPhase, "→ ")+actionLabelCLI(action))
		if action.Reason != "" {
			pln(w, paint(styMuted, "    "+clipLine(action.Reason, 100)))
		}
		if len(action.Inputs) > 0 {
			pln(w, paint(styMuted, "    needs: "+strings.Join(action.Inputs, ", ")))
		}
	}
	for _, warning := range v.ProjectionWarnings {
		pln(w, paint(styMuted, "  … "+clipLine(warning, 104)))
	}

	if len(v.Hypotheses) > 0 {
		pln(w, heading("Hypotheses"))
		for _, h := range v.Hypotheses {
			pf(w, "  %s %s\n", hypMarkCLI(h.Status), clipLine(h.Statement, 90))
		}
	}
	evidenceTotal := v.EvidenceTotal
	if evidenceTotal < len(v.Evidence) {
		evidenceTotal = len(v.Evidence)
	}
	pln(w, heading("Recent Evidence")+paint(styMuted, fmt.Sprintf("  (%d total)", evidenceTotal)))
	start := maxInt(0, len(v.Evidence)-5)
	older := maxInt(0, evidenceTotal-(len(v.Evidence)-start))
	if older > 0 {
		pln(w, paint(styMuted, fmt.Sprintf("  … %d older", older)))
	}
	for _, e := range v.Evidence[start:] {
		source := e.Source.Tool
		if source == "" {
			source = e.Source.Origin
		}
		pf(w, "  %s %s %s\n", confBadge(e.Confidence), clipLine(e.Claim, 82), paint(styMuted, source))
	}
	if len(v.PhaseDurations) > 0 {
		pln(w, heading("Time in phase")+paint(styMuted, "  (elapsed "+humanDurMs(v.ElapsedMs)+")"))
		for _, pd := range v.PhaseDurations {
			pf(w, "  %s %s\n", paint(styPhase, fmt.Sprintf("%-14s", pd.Phase)), paint(styMuted, humanDurMs(pd.Ms)))
		}
	}
	if len(v.Timeline) > 0 {
		timelineTotal := maxInt(v.TimelineTotal, len(v.Timeline))
		pln(w, heading("Recent activity")+paint(styMuted, fmt.Sprintf("  (%d total)", timelineTotal)))
		start := 0
		if len(v.Timeline) > 8 {
			start = len(v.Timeline) - 8
		}
		if older := maxInt(0, timelineTotal-(len(v.Timeline)-start)); older > 0 {
			pln(w, paint(styMuted, fmt.Sprintf("  … %d older", older)))
		}
		for _, e := range v.Timeline[start:] {
			ts := e.Timestamp.Local().Format("15:04:05")
			pln(w, "  "+paint(styMuted, ts)+"  "+timelineBadge(e.Kind)+"  "+clipLine(e.Summary, 68))
		}
	}
}

func stringIn(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func pendingSessionDecision(decisions []domain.Decision) *domain.Decision {
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			return &decisions[i]
		}
	}
	return nil
}

func verificationAssessmentLabel(a kernel.VerificationAssessment) string {
	switch a.Outcome {
	case kernel.VerificationVerified:
		return paint(styOK, "✓ verified")
	case kernel.VerificationFailed:
		return paint(styErr, "✗ failed")
	case kernel.VerificationPartial:
		return paint(styWarn, "~ partial")
	default:
		return paint(styMuted, "○ unverified")
	}
}

func verificationAssessmentGaps(a kernel.VerificationAssessment) []string {
	gaps := make([]string, 0, len(a.MissingRequired)+len(a.NonPassingClaims)+len(a.FailedClaims))
	for _, requirement := range a.MissingRequired {
		gaps = append(gaps, "missing verifier: "+requirement)
	}
	for _, claim := range a.NonPassingClaims {
		gaps = append(gaps, "not passing: "+claim)
	}
	for _, claim := range a.FailedClaims {
		gaps = append(gaps, "failed claim: "+claim)
	}
	return gaps
}

func actionLabelCLI(action domain.NextAction) string {
	if action.Command != "" {
		return action.Command
	}
	return action.Tool
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func hypMarkCLI(s domain.HypothesisStatus) string {
	switch s {
	case domain.HypConfirmed:
		return paint(styOK, "✓")
	case domain.HypRejected:
		return paint(styErr, "✗")
	case domain.HypChallenged:
		return paint(styWarn, "?")
	default:
		return paint(styMuted, "•")
	}
}

func receiptMarkCLI(s domain.VerificationStatus) string {
	switch s {
	case domain.VerifyPassed:
		return paint(styOK, "✓")
	case domain.VerifyFailed:
		return paint(styErr, "✗")
	case domain.VerifyInconclusive, domain.VerifyBlocked:
		return paint(styWarn, "~")
	default:
		return paint(styMuted, "○")
	}
}
