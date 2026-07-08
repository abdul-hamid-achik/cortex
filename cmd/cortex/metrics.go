/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var metricsCmd = &cobra.Command{
	Use:   "metrics [taskId]",
	Short: "Observability: outcome and evidence-trail metrics (SPEC §18), not just tool-call volume",
	Long: `Report Cortex's observability metrics. With a taskId, show that task's
outcome and evidence trail — tool calls, calls before first evidence, evidence
items, verification coverage by surface, unresolved hypotheses, scope drift,
memory reuse, and each tool's task-level contribution (how many hypotheses its
evidence supported). With no taskId, aggregate across every task in the
workspace (completion rate, verified-completion rate, mean tools per completed
task, scope-drift/unresolved/memory-reuse rates).`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		if len(args) == 1 {
			tm, err := k.TaskMetrics(args[0])
			if err != nil {
				return err
			}
			if jsonMode(cmd) {
				return emitJSON(tm)
			}
			renderTaskMetrics(tm)
			return nil
		}
		wm, per, err := k.WorkspaceMetrics()
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(map[string]any{"workspace": wm, "tasks": per})
		}
		renderWorkspaceMetrics(wm, per)
		return nil
	},
}

func renderTaskMetrics(m kernel.TaskMetrics) {
	w := os.Stdout
	pln(w, heading("Task "+m.TaskID))
	pf(w, "  %s %s\n", paint(styLabel, "goal   "), m.Goal)
	verdict := m.Status
	if m.Complete && m.Verified {
		verdict = paint(styOK, m.Status+" (verified)")
	} else if m.Complete {
		verdict = paint(styWarn, m.Status+" (unverified)")
	}
	pf(w, "  %s %s\n", paint(styLabel, "status "), verdict)

	pln(w, heading("Activity"))
	pf(w, "  %-26s %d\n", "tool calls", m.ToolCalls)
	pf(w, "  %-26s %d\n", "tool errors", m.ToolErrors)
	pf(w, "  %-26s %d\n", "calls before first evidence", m.CallsBeforeEvidence)
	pf(w, "  %-26s %d\n", "evidence items", m.EvidenceItems)
	pf(w, "  %-26s %d\n", "investigation rounds", m.InvestigationRounds)
	pf(w, "  %-26s %d unresolved / %d total\n", "hypotheses", m.UnresolvedHypotheses, m.Hypotheses)
	pf(w, "  %-26s %t\n", "scope drifted", m.ScopeDrifted)
	pf(w, "  %-26s %t\n", "memory reused", m.MemoryReused)

	if len(m.VerifiedSurfaces) > 0 || len(m.MissingVerification) > 0 {
		pln(w, heading("Verification coverage"))
		for _, s := range m.VerifiedSurfaces {
			pf(w, "  %s %s\n", paint(styOK, "●"), s)
		}
		for _, s := range m.MissingVerification {
			pf(w, "  %s %s (not passed)\n", paint(styErr, "○"), s)
		}
	}

	if len(m.ToolContribution) > 0 {
		pln(w, heading("Tool contribution (§18.2)"))
		for _, tc := range m.ToolContribution {
			pf(w, "  %-11s %d call(s), %d evidence → %d hypothesis(es)", tc.Tool, tc.Calls, tc.EvidenceItems, tc.HypothesesSupported)
			if tc.Errors > 0 {
				pf(w, ", %s", paint(styWarn, fmt.Sprintf("%d error(s)", tc.Errors)))
			}
			pln(w)
		}
	}
}

func renderWorkspaceMetrics(wm kernel.WorkspaceMetrics, per []kernel.TaskMetrics) {
	w := os.Stdout
	pln(w, heading("Workspace metrics"))
	pf(w, "  %-28s %d\n", "tasks", wm.Tasks)
	pf(w, "  %-28s %d (%s)\n", "completed", wm.Completed, pct(wm.CompletionRate))
	pf(w, "  %-28s %d (%s)\n", "verified completions", wm.VerifiedCompletions, pct(wm.VerifiedCompletionRate))
	pf(w, "  %-28s %.1f\n", "mean tools / completed task", wm.MeanToolsPerCompletedTask)
	pf(w, "  %-28s %s\n", "scope-drift rate", pct(wm.ScopeDriftRate))
	pf(w, "  %-28s %s\n", "unresolved-hypothesis rate", pct(wm.UnresolvedHypothesisRate))
	pf(w, "  %-28s %s\n", "memory-reuse rate", pct(wm.MemoryReuseRate))

	if len(per) > 0 {
		pln(w, heading("Tasks"))
		for _, tm := range per {
			mark := paint(styMuted, "·")
			if tm.Complete && tm.Verified {
				mark = paint(styOK, "✓")
			} else if tm.Complete {
				mark = paint(styWarn, "~")
			}
			pf(w, "  %s %-20s %-13s %d calls, %d evidence\n", mark, tm.TaskID, tm.Status, tm.ToolCalls, tm.EvidenceItems)
		}
	}
}

func pct(r float64) string { return fmt.Sprintf("%.0f%%", r*100) }

func init() {
	rootCmd.AddCommand(metricsCmd)
}
