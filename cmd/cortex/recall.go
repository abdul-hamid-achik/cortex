/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var recallCasesCmd = &cobra.Command{
	Use:   "recall-cases <query>",
	Short: "Recall prior resolved cases related to a query (cross-case disproof recall)",
	Long: `Search the cross-case recall index (veclite) for prior resolved hypotheses
(rejected/challenged are the gold) and definitive verification receipts related to a
query, scoped to a repo or cross-repo. Returns low-confidence prior disproofs to read
before re-deriving a theory. Best-effort: no veclite configured → empty, never an error.
Use --json for machine output.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		limit, _ := cmd.Flags().GetInt("limit")
		ws, _ := cmd.Flags().GetString("workspace")
		k, err := kernel.New(config.For(ws))
		if err != nil {
			return err
		}
		env, err := k.RecallCasesEnvelope(cmd.Context(), args[0], repo, limit)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(env)
		}
		renderRecall(env)
		return nil
	},
}

func init() {
	recallCasesCmd.Flags().String("repo", "", "scope recall to a repository name (empty = cross-repo)")
	recallCasesCmd.Flags().Int("limit", 5, "max prior cases to return")
	rootCmd.AddCommand(recallCasesCmd)
}

func renderRecall(env domain.Envelope) {
	w := os.Stdout
	pln(w, heading(fmt.Sprintf("Cross-case recall  (%s)", clipTo(env.Summary, 70))))
	if !env.OK {
		pf(w, "  %s\n", paint(styWarn, env.Error))
		return
	}
	if len(env.Facts) == 0 {
		pln(w, "  "+paint(styMuted, "no prior cases recalled"))
		return
	}
	for _, f := range env.Facts {
		pf(w, "  %s %s\n", paint(styMuted, "•"), f.Claim)
	}
}

// clipTo is a local truncator for the CLI (avoids pulling a kernel helper).
func clipTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
