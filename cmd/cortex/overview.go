/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var overviewCmd = &cobra.Command{
	Use:     "overview",
	Aliases: []string{"dash"},
	Short:   "Cross-repo rollup of all Cortex sessions — completion, verification, and where work sits",
	Long: `Aggregate every Cortex session across every repository into one dashboard:
totals, active/stale counts, completion and verified-completion rates, mean time
to complete, and a per-repo breakdown. Workspace-independent — the "how am I
using cortex overall" view. Use --json for machine output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		staleAfter, _ := cmd.Flags().GetDuration("stale-after")
		o, err := kernel.BuildOverview(staleAfter, time.Now())
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(o)
		}
		renderOverview(o)
		return nil
	},
}

func init() {
	overviewCmd.Flags().Duration("stale-after", 24*time.Hour, "how long before an in-flight session counts as stale")
	rootCmd.AddCommand(overviewCmd)
}

func renderOverview(o kernel.Overview) {
	w := os.Stdout
	pln(w, heading(fmt.Sprintf("Overview  (%d sessions across %d repo(s))", o.Sessions, len(o.Repos))))
	if o.Sessions == 0 {
		pln(w, "  "+paint(styMuted, "no sessions yet — start one with `cortex start \"<goal>\"`"))
		return
	}
	pf(w, "  %-22s %d\n", "active", o.Active)
	staleCell := fmt.Sprintf("%d", o.Stale)
	if o.Stale > 0 {
		staleCell = paint(styWarn, staleCell+" ⚠")
	}
	pf(w, "  %-22s %s\n", "stale", staleCell)
	pf(w, "  %-22s %d (%s)\n", "completed", o.Completed, pct(o.CompletionRate))
	pf(w, "  %-22s %d (%s)\n", "verified completions", o.Verified, pct(o.VerifiedRate))
	if o.MeanTimeToCompleteMs > 0 {
		pf(w, "  %-22s %s\n", "mean time to complete", humanDurMs(o.MeanTimeToCompleteMs))
	}

	pln(w, heading("By repo"))
	for _, r := range o.Repos {
		pf(w, "  %s %d session(s) · %d active · %d done\n",
			paint(styLabel, padRight(r.Repo, 14)), r.Sessions, r.Active, r.Completed)
	}
}
