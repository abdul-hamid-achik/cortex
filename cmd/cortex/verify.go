/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify <taskId>",
	Short: "Run the required verifiers, detect scope drift, and write receipts",
	Long: `Run verification after editing: a structural diff review (codemap), any
provided behavioral specs, and scope-drift detection. Each named claim gets a
receipt; a claim with no relevant verifier is recorded not_run — never passed.

  cortex verify task_X \
    --claim "the OAuth callback preserves the return URL" \
    --claim "users who login from checkout return to checkout" \
    --browser-spec specs/cairntrace/checkout_return.yml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		claims, _ := cmd.Flags().GetStringArray("claim")
		changed, _ := cmd.Flags().GetStringArray("changed-file")
		browser, _ := cmd.Flags().GetString("browser-spec")
		terminal, _ := cmd.Flags().GetString("terminal-spec")
		noAuto, _ := cmd.Flags().GetBool("no-auto-specs")
		env, err := k.Verify(cmd.Context(), kernel.VerifyInput{
			TaskID:           args[0],
			Claims:           claims,
			ChangedFiles:     changed,
			BrowserSpec:      browser,
			TerminalSpec:     terminal,
			DisableAutoSpecs: noAuto,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	verifyCmd.Flags().StringArray("claim", nil, "a user-facing claim to prove (repeatable)")
	verifyCmd.Flags().StringArray("changed-file", nil, "override changed files (repeatable; derived from git when omitted)")
	verifyCmd.Flags().String("browser-spec", "", "cairntrace spec path to prove browser claims")
	verifyCmd.Flags().String("terminal-spec", "", "glyphrun spec path to prove terminal claims")
	verifyCmd.Flags().Bool("no-auto-specs", false, "don't auto-select and run the specs that cover the change when none is supplied")
	rootCmd.AddCommand(verifyCmd)
}
