/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show the resolved configuration and which cortex.yaml files were applied",
	Long: `Resolve and print Cortex configuration for the workspace. Precedence,
lowest to highest: built-in defaults → global config → project .config/cortex.yaml
→ project cortex.yml/.yaml → CORTEX_* environment variables.

Configurable in cortex.yaml:
  budget: { max_parallel_calls, max_investigation_rounds, max_raw_output_bytes_per_tool,
            max_evidence_items_returned, max_candidate_files_returned, max_auto_retries_per_tool }
  redact_literals: [ ... exact strings to always mask ... ]
  cases_dir: <path>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, _ := cmd.Flags().GetString("workspace")
		cfg := config.For(ws)

		if jsonMode(cmd) {
			return emitJSON(map[string]any{
				"workspace":          cfg.Workspace,
				"casesDir":           cfg.CasesDir,
				"budget":             cfg.Budget,
				"redactLiteralCount": len(cfg.RedactLiterals),
				"sources":            cfg.Sources(),
			})
		}

		w := os.Stdout
		pln(w, heading("Configuration"))
		pf(w, "  %s %s\n", paint(styLabel, "workspace"), cfg.Workspace)
		pf(w, "  %s %s\n", paint(styLabel, "cases    "), cfg.CasesDir)
		pf(w, "  %s %d known secret literal(s)\n", paint(styLabel, "redact   "), len(cfg.RedactLiterals))

		pln(w, heading("Budget"))
		b := cfg.Budget
		pf(w, "  parallel_calls=%d  investigation_rounds=%d  raw_bytes/tool=%d\n", b.MaxParallelCalls, b.MaxInvestigationRounds, b.MaxRawOutputBytesPerTool)
		pf(w, "  evidence_items=%d  candidate_files=%d  auto_retries/tool=%d\n", b.MaxEvidenceItemsReturned, b.MaxCandidateFilesReturned, b.MaxAutoRetriesPerTool)

		pln(w, heading("Sources"))
		if src := cfg.Sources(); len(src) == 0 {
			pln(w, "  "+paint(styMuted, "built-in defaults only (no cortex.yaml found)"))
		} else {
			for _, s := range src {
				pln(w, "  "+paint(styMuted, "← ")+s)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
