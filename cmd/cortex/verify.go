/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify <taskId>",
	Short: "Run the required verifiers, detect scope drift, and write receipts",
	Long: `Run verification after editing: a structural diff review (codemap), any
provided behavioral specs, and scope-drift detection. Each named claim gets a
receipt; a claim with no relevant verifier is recorded not_run — never passed.
If begin-change claimed a lease, verification must pass the same --actor.
Prefer pairing each --claim with an explicit --claim-surface. Repository-configured
commands are arbitrary local code and run only when the trusted launcher set
CORTEX_APPROVE_COMMANDS=1; otherwise their receipts are blocked.

  cortex verify task_X \
    --claim "the OAuth callback preserves the return URL" \
    --claim-surface browser \
    --claim-contract specs/cairntrace/checkout_return.yml \
    --browser-spec specs/cairntrace/checkout_return.yml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		claims, _ := cmd.Flags().GetStringArray("claim")
		claimSurfaces, _ := cmd.Flags().GetStringArray("claim-surface")
		claimVerifiers, _ := cmd.Flags().GetStringArray("claim-verifier")
		claimContracts, _ := cmd.Flags().GetStringArray("claim-contract")
		claimSpecs, err := verificationClaimSpecs(claims, claimSurfaces, claimVerifiers, claimContracts)
		if err != nil {
			return err
		}
		if len(claimSpecs) > 0 {
			claims = nil
		}
		changed, _ := cmd.Flags().GetStringArray("changed-file")
		browser, _ := cmd.Flags().GetString("browser-spec")
		terminal, _ := cmd.Flags().GetString("terminal-spec")
		artifact, _ := cmd.Flags().GetString("artifact-ref")
		secretProject, _ := cmd.Flags().GetString("secret-project")
		noAuto, _ := cmd.Flags().GetBool("no-auto-specs")
		noOp, _ := cmd.Flags().GetBool("no-op")
		actor, _ := cmd.Flags().GetString("actor")
		env, err := k.Verify(cmd.Context(), kernel.VerifyInput{
			TaskID:           args[0],
			Actor:            actor,
			Claims:           claims,
			ClaimSpecs:       claimSpecs,
			ChangedFiles:     changed,
			BrowserSpec:      browser,
			TerminalSpec:     terminal,
			ArtifactRef:      artifact,
			SecretProject:    secretProject,
			DisableAutoSpecs: noAuto,
			NoOpAcknowledged: noOp,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	verifyCmd.Flags().StringArray("claim", nil, "a user-facing claim to prove (repeatable)")
	verifyCmd.Flags().StringArray("claim-surface", nil, "explicit surface for each --claim: code, browser, terminal, artifact, or secret")
	verifyCmd.Flags().StringArray("claim-verifier", nil, "optional exact verifier for each --claim (repeat once per claim)")
	verifyCmd.Flags().StringArray("claim-contract", nil, "required exact spec/check for each typed --claim (repeat once per claim)")
	verifyCmd.Flags().StringArray("changed-file", nil, "override changed files (repeatable; derived from git when omitted)")
	verifyCmd.Flags().String("browser-spec", "", "cairntrace spec path to prove browser claims")
	verifyCmd.Flags().String("terminal-spec", "", "glyphrun spec path to prove terminal claims")
	verifyCmd.Flags().String("artifact-ref", "", "fcheap stash ID or fcheap:// URI to prove an artifact claim")
	verifyCmd.Flags().String("secret-project", "", "tvault project whose value-free availability proves secret capability")
	verifyCmd.Flags().Bool("no-auto-specs", false, "don't auto-select and run the specs that cover the change when none is supplied")
	verifyCmd.Flags().Bool("no-op", false, "explicitly acknowledge that this change task intentionally produced no diff")
	verifyCmd.Flags().String("actor", "", "active change-lease owner, when the task is leased")
	rootCmd.AddCommand(verifyCmd)
}

func verificationClaimSpecs(claims, surfaces, verifiers, contracts []string) ([]domain.VerificationClaim, error) {
	if len(surfaces) == 0 && len(verifiers) == 0 && len(contracts) == 0 {
		return nil, nil
	}
	if len(claims) == 0 || len(surfaces) != len(claims) {
		return nil, fmt.Errorf("--claim-surface must be repeated once for every --claim")
	}
	if len(verifiers) != 0 && len(verifiers) != len(claims) {
		return nil, fmt.Errorf("--claim-verifier must be omitted or repeated once for every --claim")
	}
	if len(contracts) != len(claims) {
		return nil, fmt.Errorf("--claim-contract must be repeated once for every typed --claim")
	}
	out := make([]domain.VerificationClaim, 0, len(claims))
	for i, statement := range claims {
		claim := domain.VerificationClaim{Statement: statement, Surface: domain.Surface(surfaces[i]), Required: true}
		if len(verifiers) > 0 {
			claim.Verifier = verifiers[i]
		}
		claim.Contract = contracts[i]
		out = append(out, claim)
	}
	return out, nil
}
