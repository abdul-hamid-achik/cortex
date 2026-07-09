/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:     "show <taskId>",
	Aliases: []string{"view"},
	Short:   "Full read-only view of one session — loop, hypotheses, verification, time-in-phase, recent activity",
	Long: `Show everything about a session in one screen: its place in the reasoning
loop, hypotheses, verification receipts, time spent in each phase, and recent
activity. Works from any directory — the session is located by ID across the
central store, so you can inspect a task from another repo without cd-ing there.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := kernel.ShowSession(args[0])
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
	w := os.Stdout
	c := v.Case
	head := paint(styOK, "✓")
	if c.Status.IsTerminal() && c.Status != domain.PhaseComplete {
		head = paint(styErr, "✗")
	}
	pln(w, head+" "+paint(styPhase, "["+string(c.Status)+"]")+" "+c.Goal)
	pln(w, paint(styMuted, "  "+c.ID+" · "+v.Slug+" · "+string(c.Mode)+" · risk "+c.Risk))
	pln(w, "  "+loopStepperLine(c.Status))

	if len(v.Hypotheses) > 0 {
		pln(w, heading("Hypotheses"))
		for _, h := range v.Hypotheses {
			pf(w, "  %s %s\n", hypMarkCLI(h.Status), clipLine(h.Statement, 90))
		}
	}
	if len(v.Receipts) > 0 {
		pln(w, heading("Verification"))
		for _, r := range v.Receipts {
			pf(w, "  %s %s %s\n", receiptMarkCLI(r.Status), paint(styMuted, string(r.Surface)), clipLine(r.Claim, 78))
		}
	}
	if len(v.PhaseDurations) > 0 {
		pln(w, heading("Time in phase")+paint(styMuted, "  (elapsed "+humanDurMs(v.ElapsedMs)+")"))
		for _, pd := range v.PhaseDurations {
			pf(w, "  %s %s\n", paint(styPhase, fmt.Sprintf("%-14s", pd.Phase)), paint(styMuted, humanDurMs(pd.Ms)))
		}
	}
	if len(v.Timeline) > 0 {
		pln(w, heading("Recent activity"))
		start := 0
		if len(v.Timeline) > 8 {
			start = len(v.Timeline) - 8
		}
		for _, e := range v.Timeline[start:] {
			ts := e.Timestamp.Local().Format("15:04:05")
			pln(w, "  "+paint(styMuted, ts)+"  "+timelineBadge(e.Kind)+"  "+clipLine(e.Summary, 68))
		}
	}
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
