/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/tui"
	"github.com/spf13/cobra"
)

var studioCmd = &cobra.Command{
	Use:     "studio",
	Aliases: []string{"board", "tui"},
	Short:   "Browse case files in the interactive studio (read-only)",
	Long: `Open the Cortex studio — a Charm v2 terminal UI for browsing case files:
the task list on the left, the selected case's phase, hypotheses, evidence
ledger, and verification receipts on the right. Read-only.

Keys: ↑/↓ (or j/k) navigate · g/G jump · r refresh · q quit.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		return tui.Run(cmd.Context(), k)
	},
}

func init() {
	rootCmd.AddCommand(studioCmd)
}
