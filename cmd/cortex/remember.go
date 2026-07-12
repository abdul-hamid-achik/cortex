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
  --accept-failed  preserve an explicit failed-verification outcome`,
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
		env, err := k.Remember(cmd.Context(), kernel.RememberInput{
			TaskID:                  args[0],
			Outcome:                 joinArgs(args[1:]),
			Importance:              importance,
			Tags:                    tags,
			VerificationNotPossible: unverified,
			AcceptFailed:            acceptFailed,
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
	rootCmd.AddCommand(rememberCmd)
}
