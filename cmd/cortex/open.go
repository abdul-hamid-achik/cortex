/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open <goal>",
	Short: "Resume matching active work or start it idempotently",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		mode, _ := cmd.Flags().GetString("mode")
		risk, _ := cmd.Flags().GetString("risk")
		surfaces, _ := cmd.Flags().GetStringArray("surface")
		actor, _ := cmd.Flags().GetString("actor")
		parent, _ := cmd.Flags().GetString("parent")
		key, _ := cmd.Flags().GetString("idempotency-key")
		env, err := k.OpenTask(cmd.Context(), kernel.OpenInput{StartInput: kernel.StartInput{
			Goal: joinArgs(args), Mode: domain.Mode(mode), Risk: risk, Surfaces: toSurfaces(surfaces),
			Actor: actor, ParentTaskID: parent, IdempotencyKey: key,
		}})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	openCmd.Flags().String("mode", "change", "change | investigate | review")
	openCmd.Flags().String("risk", "medium", "low | medium | high")
	openCmd.Flags().StringArray("surface", nil, "user-visible surface (repeatable): code, browser, terminal, artifact, secret")
	openCmd.Flags().String("actor", "", "stable person or agent identifier")
	openCmd.Flags().String("parent", "", "parent task ID for delegated work")
	openCmd.Flags().String("idempotency-key", "", "stable retry key; an exact match returns the existing task, even after completion")
	rootCmd.AddCommand(openCmd)
}
