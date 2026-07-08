/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <goal>",
	Short: "Open a case for a task and orient (git identity + tool health)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		mode, _ := cmd.Flags().GetString("mode")
		risk, _ := cmd.Flags().GetString("risk")
		surfaces, _ := cmd.Flags().GetStringArray("surface")
		env, err := k.StartTask(cmd.Context(), kernel.StartInput{
			Goal:     joinArgs(args),
			Mode:     domain.Mode(mode),
			Risk:     risk,
			Surfaces: toSurfaces(surfaces),
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	startCmd.Flags().String("mode", "change", "change | investigate | review")
	startCmd.Flags().String("risk", "medium", "low | medium | high")
	startCmd.Flags().StringArray("surface", nil, "user-visible surface (repeatable): code, browser, terminal, artifact, secret")
	rootCmd.AddCommand(startCmd)
}

func toSurfaces(ss []string) []domain.Surface {
	out := make([]domain.Surface, 0, len(ss))
	for _, s := range ss {
		out = append(out, domain.Surface(s))
	}
	return out
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
