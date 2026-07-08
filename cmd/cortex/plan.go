/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan <taskId>",
	Short: "State hypotheses (with disproof), a change boundary, and a verification plan",
	Long: `Store the planning gate for a task. Each hypothesis MUST carry a disproof
path — plans without one are rejected. A change task must also declare a
change boundary (files and/or symbols).

Provide hypotheses either as paired --hypothesis / --disprove flags (matched by
position) or inline with the "statement :: disproof" form:

  cortex plan task_X \
    --hypothesis "returnTo is dropped before callback" --disprove "run login-from-checkout browser flow" \
    --file src/auth/callback.ts --symbol HandleCallback \
    --uncertainty "unsure whether state signing also strips it"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		statements, _ := cmd.Flags().GetStringArray("hypothesis")
		disproofs, _ := cmd.Flags().GetStringArray("disprove")
		confidence, _ := cmd.Flags().GetString("confidence")
		files, _ := cmd.Flags().GetStringArray("file")
		symbols, _ := cmd.Flags().GetStringArray("symbol")
		reason, _ := cmd.Flags().GetString("boundary-reason")
		verify, _ := cmd.Flags().GetStringArray("verify")
		uncertainty, _ := cmd.Flags().GetString("uncertainty")

		hyps, err := buildHypotheses(statements, disproofs, confidence)
		if err != nil {
			return err
		}
		env, err := k.Plan(kernel.PlanInput{
			TaskID:         args[0],
			Hypotheses:     hyps,
			ChangeBoundary: domain.ChangeBoundary{Files: files, Symbols: symbols, Reason: reason},
			Verification:   verify,
			Uncertainty:    uncertainty,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

// buildHypotheses pairs statements with disproofs, tolerating the inline
// "statement :: disproof" form when --disprove is not used.
func buildHypotheses(statements, disproofs []string, confidence string) ([]kernel.HypothesisInput, error) {
	if len(statements) == 0 {
		return nil, fail("at least one --hypothesis is required")
	}
	if len(disproofs) > 0 && len(disproofs) != len(statements) {
		return nil, fail("got %d --hypothesis but %d --disprove; provide one --disprove per --hypothesis", len(statements), len(disproofs))
	}
	out := make([]kernel.HypothesisInput, 0, len(statements))
	for i, s := range statements {
		stmt, dis := s, ""
		if idx := strings.Index(s, "::"); idx >= 0 {
			stmt = strings.TrimSpace(s[:idx])
			dis = strings.TrimSpace(s[idx+2:])
		}
		if i < len(disproofs) {
			dis = disproofs[i]
		}
		out = append(out, kernel.HypothesisInput{Statement: stmt, DisproveBy: dis, Confidence: confidence})
	}
	return out, nil
}

func init() {
	planCmd.Flags().StringArray("hypothesis", nil, "a hypothesis statement (repeatable; supports 'statement :: disproof')")
	planCmd.Flags().StringArray("disprove", nil, "disproof path for the matching --hypothesis (repeatable)")
	planCmd.Flags().String("confidence", "low", "confidence band for the hypotheses: high | medium | low | unknown")
	planCmd.Flags().StringArray("file", nil, "a file in the change boundary (repeatable)")
	planCmd.Flags().StringArray("symbol", nil, "a symbol in the change boundary (repeatable)")
	planCmd.Flags().String("boundary-reason", "", "why these files/symbols are the expected change set")
	planCmd.Flags().StringArray("verify", nil, "a required verifier (repeatable): codemap_review, cairntrace_flow, glyphrun_flow, …")
	planCmd.Flags().String("uncertainty", "", "explicit statement of what remains uncertain (required)")
	rootCmd.AddCommand(planCmd)
}
