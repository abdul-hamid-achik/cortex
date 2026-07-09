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
across every repository: the session list on the left, and the selected case's
loop progress (orient→…→preserve), hypotheses, evidence ledger, and verification
receipts on the right. Auto-refreshes; read-only.

Keys: ↑/↓ (or j/k) navigate · g/G jump · a active-only · r refresh · q quit.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		active, _ := cmd.Flags().GetBool("active")
		return tui.Run(cmd.Context(), kernel.SessionFilter{Repo: repo, ActiveOnly: active})
	},
}

func init() {
	studioCmd.Flags().String("repo", "", "only sessions whose repository or slug contains this substring")
	studioCmd.Flags().Bool("active", false, "start showing only in-flight (non-terminal) sessions")
	rootCmd.AddCommand(studioCmd)
}
