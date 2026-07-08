/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

// pln / pf write a line to a terminal writer, discarding the write error —
// stdout/stderr write failures are not actionable for a CLI and checking them
// everywhere adds noise (this keeps errcheck clean at the one honest place).
func pln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func pf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

// Charm v2 lipgloss styles. Colors are ANSI indices so the output degrades
// gracefully on limited terminals and honors the user's theme.
var (
	styLabel   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	styOK      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	styErr     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	styWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styPhase   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	styFact    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
)

// useColor gates ANSI styling: only when stdout is a real terminal and NO_COLOR
// is unset. When piped to a file/agent, output is plain (the --json path is the
// machine surface). This avoids per-grapheme escape sequences in non-TTY sinks.
var useColor = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// paint applies a style only when color is enabled, else returns plain text.
func paint(s lipgloss.Style, text string) string {
	if !useColor {
		return text
	}
	return s.Render(text)
}

// emitEnvelope prints a result envelope as JSON (--json) or a styled view.
func emitEnvelope(cmd *cobra.Command, env domain.Envelope) error {
	if jsonMode(cmd) {
		return emitJSON(env)
	}
	renderEnvelope(os.Stdout, env)
	if !env.OK {
		return fail("%s", env.Error)
	}
	return nil
}

// emitJSON prints any value as indented JSON without HTML escaping.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func renderEnvelope(w *os.File, env domain.Envelope) {
	head := paint(styOK, "✓")
	if !env.OK {
		head = paint(styErr, "✗")
	}
	line := head + " "
	if env.Phase != "" {
		line += paint(styPhase, "["+string(env.Phase)+"]") + " "
	}
	line += env.Summary
	pln(w, line)
	if env.TaskID != "" {
		pln(w, paint(styMuted, "  task "+env.TaskID))
	}

	if len(env.Facts) > 0 {
		pln(w, heading("Evidence"))
		for _, f := range env.Facts {
			pf(w, "  %s %s %s\n",
				paint(styMuted, f.ID),
				confBadge(f.Confidence),
				paint(styFact, clipLine(f.Claim, 100)))
		}
	}
	if len(env.Hypotheses) > 0 {
		pln(w, heading("Hypotheses"))
		for _, h := range env.Hypotheses {
			pf(w, "  %s %s %s\n", paint(styMuted, h.ID), confBadge(h.Confidence), clipLine(h.Statement, 100))
		}
	}
	if len(env.Warnings) > 0 {
		pln(w, heading("Warnings"))
		for _, warn := range env.Warnings {
			pln(w, "  "+paint(styWarn, "⚠ "+clipLine(warn, 160)))
		}
	}
	if len(env.NextActions) > 0 {
		pln(w, heading("Next"))
		for _, n := range env.NextActions {
			pln(w, "  "+paint(styMuted, "→ ")+n)
		}
	}
}

// heading renders a blank line then a bold section label.
func heading(s string) string { return "\n" + paint(styHeading, s) }

// renderStatus prints the detailed status view.
func renderStatus(rep kernel.StatusReport) {
	w := os.Stdout
	renderEnvelope(w, rep.Envelope)
	pln(w, heading("Task"))
	pf(w, "  %s %s · %s %s · %s %s\n",
		paint(styLabel, "mode"), rep.Mode, paint(styLabel, "risk"), rep.Risk, paint(styLabel, "repo"), rep.Workspace.Repository)
	if rep.Workspace.Branch != "" {
		pf(w, "  %s %s @ %s\n", paint(styLabel, "branch"), rep.Workspace.Branch, rep.Workspace.CommitBefore)
	}
	pf(w, "  %s %d\n", paint(styLabel, "evidence"), rep.EvidenceCount)
	if rep.InvestigationBudget > 0 {
		rounds := fmt.Sprintf("%d/%d", rep.InvestigationRounds, rep.InvestigationBudget)
		if rep.InvestigationRounds > rep.InvestigationBudget {
			rounds = paint(styWarn, rounds+" (over budget)")
		}
		pf(w, "  %s %s\n", paint(styLabel, "rounds  "), rounds)
	}

	if len(rep.UnresolvedHypotheses) > 0 {
		pln(w, heading("Unresolved hypotheses"))
		for _, h := range rep.UnresolvedHypotheses {
			pf(w, "  %s %s\n", confBadge(h.Confidence), clipLine(h.Statement, 100))
		}
	}
	if len(rep.MissingVerification) > 0 {
		pln(w, heading("Missing verification"))
		for _, m := range rep.MissingVerification {
			pln(w, "  "+paint(styWarn, "✗ "+m))
		}
	}
	if rep.Scope != nil && rep.Scope.Scope == "drift_detected" {
		pln(w, heading("Scope drift"))
		pf(w, "  %s risk — %s\n", rep.Scope.Risk, rep.Scope.Action)
		for _, f := range rep.Scope.UnexpectedFiles {
			pln(w, "    "+paint(styWarn, f))
		}
	}
	if len(rep.ToolHealth) > 0 {
		pln(w, heading("Tool health"))
		for _, h := range rep.ToolHealth {
			mark := paint(styOK, "●")
			if !h.Available {
				mark = paint(styErr, "○")
			}
			pf(w, "  %s %s %s\n", mark, h.Tool, paint(styMuted, h.Detail))
		}
	}
}

func confBadge(c domain.Confidence) string {
	switch c {
	case domain.ConfidenceHigh:
		return paint(styOK, "[high]")
	case domain.ConfidenceMedium:
		return paint(styWarn, "[med]")
	case domain.ConfidenceLow:
		return paint(styMuted, "[low]")
	default:
		return paint(styMuted, "[?]")
	}
}

func clipLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
