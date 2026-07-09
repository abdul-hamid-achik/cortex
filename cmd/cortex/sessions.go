/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var sessionsCmd = &cobra.Command{
	Use:     "sessions",
	Aliases: []string{"sess"},
	Short:   "List Cortex sessions across every repository (the central XDG audit view)",
	Long: "List every session under the central state tree ($XDG_STATE_HOME/cortex/sessions), " +
		"newest first — one place to audit and monitor all your Cortex work regardless of which " +
		"repo it belongs to. Filter with --repo (substring) and --active (in-flight only). " +
		"Sessions pinned repo-local via cases_dir aren't shown here; use `cortex list` in that repo.",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		active, _ := cmd.Flags().GetBool("active")
		staleOnly, _ := cmd.Flags().GetBool("stale")
		staleAfter, _ := cmd.Flags().GetDuration("stale-after")
		archived, _ := cmd.Flags().GetBool("archived")

		lister, root := kernel.AllSessions, config.SessionsRoot()
		if archived {
			lister, root = kernel.ArchivedSessions, config.ArchiveRoot()
		}
		sessions, err := lister(kernel.SessionFilter{Repo: repo, ActiveOnly: active})
		if err != nil {
			return err
		}
		now := time.Now()
		if staleOnly {
			kept := sessions[:0]
			for _, s := range sessions {
				if s.StaleSince(now, staleAfter) {
					kept = append(kept, s)
				}
			}
			sessions = kept
			if len(sessions) == 0 && !jsonMode(cmd) {
				pln(os.Stdout, paint(styOK, "✓ no stale sessions — all in-flight work is fresh"))
				return nil
			}
		}
		if jsonMode(cmd) {
			return emitJSON(sessions)
		}
		renderSessions(sessions, now, staleAfter, root)
		return nil
	},
}

func init() {
	sessionsCmd.Flags().String("repo", "", "only sessions whose repository or slug contains this substring")
	sessionsCmd.Flags().Bool("active", false, "only in-flight (non-terminal) sessions")
	sessionsCmd.Flags().Bool("stale", false, "only in-flight sessions untouched beyond --stale-after")
	sessionsCmd.Flags().Duration("stale-after", 24*time.Hour, "how long before an in-flight session is flagged stale")
	sessionsCmd.Flags().Bool("archived", false, "list archived (retired) sessions instead of active ones")
	rootCmd.AddCommand(sessionsCmd)
}

func renderSessions(sessions []kernel.SessionSummary, now time.Time, staleAfter time.Duration, root string) {
	w := os.Stdout
	pln(w, paint(styMuted, "sessions in "+root))
	if len(sessions) == 0 {
		pln(w, paint(styMuted, "no sessions yet — start one with `cortex start \"<goal>\"`"))
		return
	}
	repoW := len("REPO")
	for _, s := range sessions {
		if n := len(s.Slug); n > repoW {
			repoW = n
		}
	}
	if repoW > 24 {
		repoW = 24
	}
	// Header (pad the plain text, then paint the padded span so ANSI escapes
	// don't throw off column widths — the same trick `list` uses).
	pf(w, "%s  %s  %s  %s  %s\n",
		paint(styHeading, padRight("REPO", repoW)),
		paint(styHeading, padRight("PHASE", 13)),
		paint(styHeading, padRight("AGE", 5)),
		paint(styHeading, padRight("VERIF", 5)),
		paint(styHeading, "GOAL"))
	for _, s := range sessions {
		ageStyle, ageCell := styMuted, humanAge(s.UpdatedAt)
		if s.StaleSince(now, staleAfter) {
			ageStyle, ageCell = styWarn, ageCell+"⚠" // in-flight but untouched — likely forgotten
		}
		pf(w, "%s  %s  %s  %s  %s\n",
			paint(styLabel, padRight(s.Slug, repoW)),
			paint(phaseStyle(s.Phase), padRight(string(s.Phase), 13)),
			paint(ageStyle, padRight(ageCell, 5)),
			paint(verifStyle(s), padRight(verifCell(s), 5)),
			clipLine(s.Goal, 56))
	}
}

// phaseStyle colors a phase by outcome class: green complete, red terminal-bad,
// cyan in-flight (mirrors the studio board's phaseColor).
func phaseStyle(p domain.Phase) lipgloss.Style {
	switch p {
	case domain.PhaseComplete:
		return styOK
	case domain.PhaseBlocked, domain.PhaseAbandoned, domain.PhaseNeedsHumanDecision:
		return styErr
	default:
		return styPhase
	}
}

func verifCell(s kernel.SessionSummary) string {
	if s.Required == 0 && s.Verified == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", s.Verified, s.Required)
}

func verifStyle(s kernel.SessionSummary) lipgloss.Style {
	switch {
	case s.Required > 0 && s.Verified >= s.Required:
		return styOK
	case s.Required > 0:
		return styWarn
	default:
		return styMuted
	}
}

// humanAge renders a compact relative age (now/4m/2h/3d) from a timestamp.
func humanAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// padRight left-justifies s to n runes, truncating with an ellipsis when longer.
func padRight(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		if n <= 1 {
			return string(r[:n])
		}
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}
