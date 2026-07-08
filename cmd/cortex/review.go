/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Evidence-backed review of a branch or pull request (diff-scoped)",
	Long: `Review a branch or pull request as a Cortex task: resolve the diff
(base…HEAD), gather structural and semantic context, run the verifiers over the
change (structural review plus the behavioral specs that cover it), and produce
a verdict — approve / request-changes / needs-verification — where every claim
is backed by a receipt you can inspect with 'cortex status <taskId> --detail full'.

  cortex review                       # current branch vs its fork point with the default branch
  cortex review --base release/2.1    # vs a specific base
  cortex review --pr 42               # fetch + review a pull request (GitHub or Bitbucket)
  cortex review --surface browser     # also auto-run the browser specs that cover the change

A pull request is fetched host-agnostically by git ref (GitHub pull/N/head,
Bitbucket pull-requests/N/from); when a host can't be fetched by ref, Cortex
tells you to check out the branch and re-run with --base.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		base, _ := cmd.Flags().GetString("base")
		head, _ := cmd.Flags().GetString("head")
		pr, _ := cmd.Flags().GetInt("pr")
		risk, _ := cmd.Flags().GetString("risk")
		claims, _ := cmd.Flags().GetStringArray("claim")
		surfaceStrs, _ := cmd.Flags().GetStringArray("surface")
		var surfaces []domain.Surface
		for _, s := range surfaceStrs {
			surfaces = append(surfaces, domain.Surface(s))
		}
		env, err := k.Review(cmd.Context(), kernel.ReviewInput{
			Base: base, Head: head, PR: pr, Risk: risk, Claims: claims, Surfaces: surfaces,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	reviewCmd.Flags().String("base", "", "base ref to diff from (default: merge-base with the default branch)")
	reviewCmd.Flags().String("head", "", "ref to review (default: current branch)")
	reviewCmd.Flags().Int("pr", 0, "pull/merge request number to fetch and review")
	reviewCmd.Flags().String("risk", "medium", "risk band: low | medium | high")
	reviewCmd.Flags().StringArray("surface", nil, "surface to check (repeatable): code, browser, terminal")
	reviewCmd.Flags().StringArray("claim", nil, "an additional claim to prove (repeatable)")
	rootCmd.AddCommand(reviewCmd)
}
