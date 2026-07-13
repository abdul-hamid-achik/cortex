/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"strconv"
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
		supports, _ := cmd.Flags().GetStringArray("support")
		confidence, _ := cmd.Flags().GetString("confidence")
		files, _ := cmd.Flags().GetStringArray("file")
		symbols, _ := cmd.Flags().GetStringArray("symbol")
		reason, _ := cmd.Flags().GetString("boundary-reason")
		verify, _ := cmd.Flags().GetStringArray("verify")
		uncertainty, _ := cmd.Flags().GetString("uncertainty")
		timeoutFlags, _ := cmd.Flags().GetStringArray("timeout")

		hyps, err := buildHypotheses(statements, disproofs, confidence)
		if err != nil {
			return err
		}
		if err := applyHypothesisSupports(hyps, supports); err != nil {
			return err
		}
		timeouts, err := parseTimeouts(timeoutFlags)
		if err != nil {
			return err
		}
		env, err := k.Plan(kernel.PlanInput{
			TaskID:           args[0],
			Hypotheses:       hyps,
			ChangeBoundary:   domain.ChangeBoundary{Files: files, Symbols: symbols, Reason: reason},
			Verification:     verify,
			Uncertainty:      uncertainty,
			TimeoutOverrides: timeouts,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

// applyHypothesisSupports attaches evidence IDs using a strict one-based
// "hypothesis-index=evidence-id[,evidence-id...]" syntax. Explicit indexes
// avoid the positional ambiguity of optional repeated flags when hypotheses
// cite different numbers of evidence records.
func applyHypothesisSupports(hypotheses []kernel.HypothesisInput, flags []string) error {
	seen := make([]map[string]bool, len(hypotheses))
	for _, flag := range flags {
		indexText, evidenceText, ok := strings.Cut(flag, "=")
		index, err := strconv.Atoi(strings.TrimSpace(indexText))
		if !ok || err != nil || index < 1 || index > len(hypotheses) || strings.TrimSpace(evidenceText) == "" {
			return fail("invalid --support %q; expected hypothesis-index=evidence-id[,evidence-id...] with index 1..%d", flag, len(hypotheses))
		}
		if seen[index-1] == nil {
			seen[index-1] = make(map[string]bool)
			for _, existing := range hypotheses[index-1].Supports {
				seen[index-1][existing] = true
			}
		}
		for _, rawID := range strings.Split(evidenceText, ",") {
			evidenceID := strings.TrimSpace(rawID)
			if evidenceID == "" {
				return fail("invalid --support %q; evidence ids must be non-empty", flag)
			}
			if seen[index-1][evidenceID] {
				return fail("duplicate --support evidence %q for hypothesis %d", evidenceID, index)
			}
			seen[index-1][evidenceID] = true
			hypotheses[index-1].Supports = append(hypotheses[index-1].Supports, evidenceID)
		}
	}
	return nil
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

// parseTimeouts turns repeated "tool=duration" flags into a per-tool timeout
// override map. Invalid or duplicate entries fail explicitly so a
// caller never believes an override was applied when it was silently dropped.
func parseTimeouts(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(flags))
	for _, f := range flags {
		parts := strings.SplitN(f, "=", 2)
		if len(parts) != 2 {
			return nil, fail("invalid --timeout %q; expected tool=duration", f)
		}
		tool := strings.ToLower(strings.TrimSpace(parts[0]))
		duration := strings.TrimSpace(parts[1])
		if tool == "" || duration == "" {
			return nil, fail("invalid --timeout %q; expected non-empty tool=duration", f)
		}
		if _, exists := m[tool]; exists {
			return nil, fail("duplicate --timeout for tool %q", tool)
		}
		m[tool] = duration
	}
	return m, nil
}

func init() {
	planCmd.Flags().StringArray("hypothesis", nil, "a hypothesis statement (repeatable; supports 'statement :: disproof')")
	planCmd.Flags().StringArray("disprove", nil, "disproof path for the matching --hypothesis (repeatable)")
	planCmd.Flags().StringArray("support", nil, "evidence support as hypothesis-index=evidence-id[,evidence-id...] (repeatable)")
	planCmd.Flags().String("confidence", "low", "confidence band for the hypotheses: high | medium | low | unknown")
	planCmd.Flags().StringArray("file", nil, "a file in the change boundary (repeatable)")
	planCmd.Flags().StringArray("symbol", nil, "a symbol in the change boundary (repeatable)")
	planCmd.Flags().String("boundary-reason", "", "why these files/symbols are the expected change set")
	planCmd.Flags().StringArray("verify", nil, "a required verifier (repeatable): codemap_review, cairntrace_flow, glyphrun_flow, …")
	planCmd.Flags().StringArray("timeout", nil, "per-task timeout override as tool=duration (repeat once per tool, e.g. codemap=45s)")
	planCmd.Flags().String("uncertainty", "", "explicit statement of what remains uncertain (required)")
	rootCmd.AddCommand(planCmd)
}
