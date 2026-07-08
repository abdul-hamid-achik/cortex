/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var resolveCmd = &cobra.Command{
	Use:   "resolve <taskId> <hypothesisId>",
	Short: "Update a hypothesis's status as evidence accumulates (confirm/challenge/reject)",
	Long: `Resolve a hypothesis without erasing history. As investigation and
verification produce evidence, mark a hypothesis confirmed, challenged, or
rejected — the prior state is retained and the resolution is appended to the
evidence ledger with your reason (SPEC §9.3 contradiction handling).

  cortex resolve task_06FK… hyp_06FK… --status rejected \
    --reason "the browser flow returned to checkout, so returnTo was NOT dropped"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		status, _ := cmd.Flags().GetString("status")
		reason, _ := cmd.Flags().GetString("reason")
		evidence, _ := cmd.Flags().GetStringArray("evidence")
		env, err := k.Resolve(kernel.ResolveInput{
			TaskID:       args[0],
			HypothesisID: args[1],
			Status:       status,
			Reason:       reason,
			Evidence:     evidence,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	resolveCmd.Flags().String("status", "", "confirmed | challenged | rejected (required)")
	resolveCmd.Flags().String("reason", "", "what evidence changed the status (required)")
	resolveCmd.Flags().StringArray("evidence", nil, "supporting/contradicting evidence IDs (repeatable)")
	_ = resolveCmd.MarkFlagRequired("status")
	rootCmd.AddCommand(resolveCmd)
}
