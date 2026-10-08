/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var rememberCmd = &cobra.Command{
	Use:     "remember <taskId> <outcome>",
	Aliases: []string{"complete"},
	Short:   "Persist the outcome to durable memory and complete the task",
	Long: `Complete a task by persisting a concise, provenance-rich outcome. A task
normally completes only when its canonical assessment is verified. Explicit acknowledgments:

  --unverified     preserve a partial/unverified outcome when adequate proof could not be completed
                   (registered acceptance criteria still need proof or --accept-missing-criteria)
  --accept-failed  preserve an explicit failed-verification outcome
  --accept-missing-criteria ids  record exactly the unproven registered criteria as unmet (never verified)`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		importance, _ := cmd.Flags().GetFloat64("importance")
		tags, _ := cmd.Flags().GetStringArray("tag")
		unverified, _ := cmd.Flags().GetBool("unverified")
		acceptFailed, _ := cmd.Flags().GetBool("accept-failed")
		acceptChildren, _ := cmd.Flags().GetBool("accept-open-children")
		acceptCoverage, _ := cmd.Flags().GetBool("accept-partial-coverage")
		acceptMissing, _ := cmd.Flags().GetStringSlice("accept-missing-criteria")
		env, err := k.Remember(cmd.Context(), kernel.RememberInput{
			TaskID:                    args[0],
			Outcome:                   joinArgs(args[1:]),
			Importance:                importance,
			Tags:                      tags,
			VerificationNotPossible:   unverified,
			AcceptFailed:              acceptFailed,
			AcceptOpenChildren:        acceptChildren,
			AcceptPartialCoverage:     acceptCoverage,
			CriteriaUnmetAcknowledged: acceptMissing,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	rememberCmd.Flags().Float64("importance", 0.5, "0..1 importance for durable memory")
	rememberCmd.Flags().StringArray("tag", nil, "tag for recall (repeatable)")
	rememberCmd.Flags().Bool("unverified", false, "complete with an explicit partial/unverified assessment acknowledgment")
	rememberCmd.Flags().Bool("accept-failed", false, "complete with an explicit failed-verification acknowledgment")
	rememberCmd.Flags().Bool("accept-open-children", false, "complete a parent while child tasks are still in-flight")
	rememberCmd.Flags().Bool("accept-partial-coverage", false, "complete a survey while ledger modules remain unseen")
	rememberCmd.Flags().StringSlice("accept-missing-criteria", nil, "record exactly these registered acceptance criteria (comma-separated ids) as unmet; the outcome can then only be partial")
	rootCmd.AddCommand(rememberCmd)
}
