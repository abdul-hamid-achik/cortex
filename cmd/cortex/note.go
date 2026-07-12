/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var noteCmd = &cobra.Command{
	Use:   "note <taskId> <observation>",
	Short: "Attach a human/agent observation, decision, or constraint to a case",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		category, _ := cmd.Flags().GetString("kind")
		origin, _ := cmd.Flags().GetString("origin")
		actor, _ := cmd.Flags().GetString("actor")
		ref, _ := cmd.Flags().GetString("ref")
		file, _ := cmd.Flags().GetString("file")
		line, _ := cmd.Flags().GetInt("line")
		confidence, _ := cmd.Flags().GetString("confidence")
		sensitive, _ := cmd.Flags().GetBool("sensitive")
		var location *domain.Location
		if file != "" {
			location = &domain.Location{File: file, StartLine: line}
		}
		env, err := k.RecordObservation(kernel.ObservationInput{
			TaskID: args[0], Claim: joinArgs(args[1:]), Category: category,
			Origin: origin, Actor: actor, URI: ref, Location: location,
			Confidence: confidence, Sensitive: sensitive,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	noteCmd.Flags().String("kind", "observation", "observation | decision | constraint | handoff")
	noteCmd.Flags().String("origin", "human", "human | agent | reviewer")
	noteCmd.Flags().String("actor", "", "human or agent name recorded as provenance")
	noteCmd.Flags().String("ref", "", "source URI/reference for the observation")
	noteCmd.Flags().String("file", "", "source file related to the observation")
	noteCmd.Flags().Int("line", 0, "source line (with --file)")
	noteCmd.Flags().String("confidence", "medium", "low | medium")
	noteCmd.Flags().Bool("sensitive", false, "mark the observation as sensitive")
	noteCmd.ValidArgsFunction = completeTaskIDs
	rootCmd.AddCommand(noteCmd)
}
