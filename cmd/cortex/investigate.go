/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var investigateCmd = &cobra.Command{
	Use:     "investigate <taskId> [question]",
	Aliases: []string{"inv"},
	Short:   "Route a question through discovery then structure; record evidence",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		surfaces, _ := cmd.Flags().GetStringArray("surface")
		depth, _ := cmd.Flags().GetString("depth")
		video, _ := cmd.Flags().GetString("video")
		module, _ := cmd.Flags().GetString("module")
		async, _ := cmd.Flags().GetBool("async")
		fanout, _ := cmd.Flags().GetBool("fanout")
		max, _ := cmd.Flags().GetInt("max")
		if async || fanout {
			var modules []string
			if module != "" {
				modules = []string{module}
			}
			rep, err := k.StartJob(cmd.Context(), kernel.JobInput{
				TaskID: args[0], Question: joinArgs(args[1:]), Depth: depth, Modules: modules, Fanout: fanout, Max: max,
			})
			if err != nil {
				return err
			}
			return emitReport(cmd, rep.Envelope, rep, func() { renderJobs(rep) })
		}
		env, err := k.Investigate(cmd.Context(), kernel.InvestigateInput{
			TaskID:   args[0],
			Question: joinArgs(args[1:]),
			Surfaces: toSurfaces(surfaces),
			Depth:    depth,
			Video:    video,
			Module:   module,
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
	investigateCmd.Flags().String("module", "", "scope discovery to one directory and record survey coverage for it")
	investigateCmd.Flags().Bool("async", false, "run the round in a detached background job (poll with cortex job list)")
	investigateCmd.Flags().Bool("fanout", false, "survey: queue one background round per unseen module (question optional)")
	investigateCmd.Flags().Int("max", 0, "fan-out size (default 5, max 32)")
	rootCmd.AddCommand(investigateCmd)
}
