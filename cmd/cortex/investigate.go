/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var investigateCmd = &cobra.Command{
	Use:     "investigate <taskId> <question>",
	Aliases: []string{"inv"},
	Short:   "Route a question through discovery then structure; record evidence",
	Args:    cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		surfaces, _ := cmd.Flags().GetStringArray("surface")
		depth, _ := cmd.Flags().GetString("depth")
		video, _ := cmd.Flags().GetString("video")
		env, err := k.Investigate(cmd.Context(), kernel.InvestigateInput{
			TaskID:   args[0],
			Question: joinArgs(args[1:]),
			Surfaces: toSurfaces(surfaces),
			Depth:    depth,
			Video:    video,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	investigateCmd.Flags().StringArray("surface", nil, "override routing surfaces (repeatable)")
	investigateCmd.Flags().String("depth", "standard", "quick | standard | deep")
	investigateCmd.Flags().String("video", "", "a bug-video bundle path or vidtrace stash id to investigate (runs vidtrace → code)")
	rootCmd.AddCommand(investigateCmd)
}
