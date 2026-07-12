/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"sort"

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
  cases_dir: <path>
  recall: { enabled, db_path, embed_model, embed_url }
  verifiers: { <name>: { argv, kind, surface, timeout } }

The resolved view lists verifier policy but deliberately omits executable argv.
Configured commands remain blocked unless the trusted process launching Cortex
sets CORTEX_APPROVE_COMMANDS=1; repository configuration cannot approve itself.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, _ := cmd.Flags().GetString("workspace")
		cfg := config.For(ws)
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("invalid cortex configuration: %w", err)
		}
		verifiers := safeVerifierViews(cfg.Verifiers)
		recall := map[string]any{
			"enabled": cfg.Recall.Enabled, "dbPath": cfg.Recall.DBPath,
			"embedModel": cfg.Recall.EmbedModel, "embedUrl": cfg.Recall.EmbedURL,
		}

		if jsonMode(cmd) {
			return emitJSON(map[string]any{
				"workspace":          cfg.Workspace,
				"casesDir":           cfg.CasesDir,
				"configDir":          config.ConfigDir(),
				"sessionsRoot":       config.SessionsRoot(),
				"archiveRoot":        config.ArchiveRoot(),
				"cacheDir":           config.CacheHome(),
				"budget":             cfg.Budget,
				"recall":             recall,
				"verifiers":          verifiers,
				"redactLiteralCount": len(cfg.RedactLiterals),
				"sources":            cfg.Sources(),
			})
		}

		w := os.Stdout
		pln(w, heading("Configuration"))
		pf(w, "  %s %s\n", paint(styLabel, "workspace"), cfg.Workspace)
		pf(w, "  %s %s\n", paint(styLabel, "cases    "), cfg.CasesDir)
		pf(w, "  %s %d known secret literal(s)\n", paint(styLabel, "redact   "), len(cfg.RedactLiterals))

		// The global XDG layout — where Cortex keeps everything, so it can be
		// audited/backed up/pointed elsewhere (CORTEX_HOME or a legacy ~/.cortex
		// collapses these into one dir).
		pln(w, heading("Storage (XDG)"))
		pf(w, "  %s %s\n", paint(styLabel, "config  "), config.ConfigDir())
		pf(w, "  %s %s\n", paint(styLabel, "sessions"), config.SessionsRoot())
		pf(w, "  %s %s\n", paint(styLabel, "archive "), config.ArchiveRoot())
		pf(w, "  %s %s\n", paint(styLabel, "cache   "), config.CacheHome())

		pln(w, heading("Budget"))
		b := cfg.Budget
		pf(w, "  parallel_calls=%d  investigation_rounds=%d  raw_bytes/tool=%d\n", b.MaxParallelCalls, b.MaxInvestigationRounds, b.MaxRawOutputBytesPerTool)
		pf(w, "  evidence_items=%d  candidate_files=%d  auto_retries/tool=%d\n", b.MaxEvidenceItemsReturned, b.MaxCandidateFilesReturned, b.MaxAutoRetriesPerTool)

		pln(w, heading("Recall"))
		pf(w, "  enabled=%t  model=%s\n", cfg.Recall.Enabled, cfg.Recall.EmbedModel)
		pf(w, "  %s %s\n", paint(styLabel, "database"), cfg.Recall.DBPath)
		pf(w, "  %s %s\n", paint(styLabel, "endpoint"), cfg.Recall.EmbedURL)

		pln(w, heading("Configured verifiers"))
		if len(verifiers) == 0 {
			pln(w, "  "+paint(styMuted, "none"))
		} else {
			for _, verifier := range verifiers {
				pf(w, "  %s  kind=%s  surface=%s  timeout=%s  %s\n",
					paint(styLabel, verifier.Name), verifier.Kind, verifier.Surface, verifier.Timeout,
					paint(styMuted, "argv hidden"))
			}
		}

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

type safeVerifierView struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Surface string `json:"surface"`
	Timeout string `json:"timeout"`
}

func safeVerifierViews(verifiers map[string]config.CommandVerifier) []safeVerifierView {
	names := make([]string, 0, len(verifiers))
	for name := range verifiers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]safeVerifierView, 0, len(names))
	for _, name := range names {
		verifier := verifiers[name]
		out = append(out, safeVerifierView{
			Name: name, Kind: string(verifier.Kind), Surface: string(verifier.Surface), Timeout: verifier.Timeout.String(),
		})
	}
	return out
}

func init() {
	rootCmd.AddCommand(configCmd)
}
