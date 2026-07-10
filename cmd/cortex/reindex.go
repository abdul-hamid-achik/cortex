/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var reindexCasesCmd = &cobra.Command{
	Use:   "reindex-cases",
	Short: "Rebuild cross-case recall from active central sessions",
	Long: `Rebuild the cross-case recall index from $XDG_STATE_HOME/cortex/sessions.
Archives and repo-local cases_dir overrides are deliberately excluded. Individual
session and record failures are reported while the remaining cases continue.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		report, err := k.ReindexCases(cmd.Context())
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			if err := emitJSON(report); err != nil {
				return err
			}
		} else {
			renderReindexCases(report)
		}
		if report.Failed != 0 || report.SessionLoadFailed != 0 {
			return fail("reindex completed with %d record failure(s) and %d session load failure(s)", report.Failed, report.SessionLoadFailed)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reindexCasesCmd)
}

func renderReindexCases(report kernel.ReindexCasesReport) {
	w := os.Stdout
	pln(w, heading("Cross-case recall reindex"))
	pf(w, "  sessions  %d scanned, %d failed to load\n", report.SessionsScanned, report.SessionLoadFailed)
	pf(w, "  records   %d scanned, %d indexed, %d skipped, %d failed\n",
		report.RecordsScanned, report.Indexed, report.Skipped, report.Failed)
	for _, warning := range report.Warnings {
		pf(w, "  %s %s\n", paint(styWarn, "warning"), warning)
	}
	if report.Failed == 0 && report.SessionLoadFailed == 0 {
		pln(w, paint(styOK, fmt.Sprintf("✓ indexed %d record(s)", report.Indexed)))
	}
}
