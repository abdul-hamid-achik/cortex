/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/abdul-hamid-achik/cortex/internal/tui"
	"github.com/spf13/cobra"
)

var studioCmd = &cobra.Command{
	Use:     "studio",
	Aliases: []string{"board", "tui"},
	Short:   "Live board of all Cortex sessions across every repo (read-only)",
	Long: `Open the Cortex studio — a live Charm v2 terminal board of every session
across every repository. Wide terminals show the session list and selected case
side by side; narrow terminals stack them. The detail pane contains loop progress
(orient→…→preserve), canonical verification assessment and gaps, pending decision,
first structured next action, hypotheses, recent evidence, and recent receipts.
Auto-refreshes; read-only.

Studio is interactive and deliberately does not support --json. Use
cortex sessions --json or cortex show <taskId> --json for machine output.

Keys: ↑/↓ (or j/k) navigate · PgUp/PgDn (or Ctrl-U/Ctrl-D) scroll detail ·
g/G jump · / search · c clear search · a active-only · r refresh · q quit.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if jsonMode(cmd) {
			return fail("studio is interactive and does not support --json; use cortex sessions --json or cortex show <taskId> --json")
		}
		repo, _ := cmd.Flags().GetString("repo")
		query, _ := cmd.Flags().GetString("query")
		active, _ := cmd.Flags().GetBool("active")
		return tui.Run(cmd.Context(), kernel.SessionFilter{Repo: repo, ActiveOnly: active, Query: query})
	},
}

func init() {
	studioCmd.Flags().String("repo", "", "only sessions whose repository or slug contains this substring")
	studioCmd.Flags().String("query", "", "start with a session search (all whitespace-separated tokens must match)")
	studioCmd.Flags().Bool("active", false, "start showing only in-flight (non-terminal) sessions")
	rootCmd.AddCommand(studioCmd)
}
