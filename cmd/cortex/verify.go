/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

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
If begin-change claimed a lease, --actor defaults to the lease owner; pass
--actor explicitly only to assert a specific actor (it must match the owner).
Prefer pairing each --claim with an explicit --claim-surface. Repository-configured
commands are arbitrary local code and run only when the trusted launcher set
CORTEX_APPROVE_COMMANDS=1; otherwise their receipts are blocked.

  cortex verify task_X \
    --claim "the OAuth callback preserves the return URL" \
    --claim-id checkout_return \
    --claim-surface browser \
    --claim-contract specs/cairntrace/checkout_return.yml \
    --browser-spec specs/cairntrace/checkout_return.yml

Each typed claim can also be given as ONE self-contained --claim-spec instead of
the coupled --claim-id/-surface/-verifier/-contract flags (repeat per claim):

  cortex verify task_X \
    --claim-spec "id=checkout_return|surface=browser|contract=specs/cairntrace/checkout_return.yml|the OAuth callback preserves the return URL" \
    --browser-spec specs/cairntrace/checkout_return.yml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		claims, _ := cmd.Flags().GetStringArray("claim")
		claimIDs, _ := cmd.Flags().GetStringArray("claim-id")
		claimSurfaces, _ := cmd.Flags().GetStringArray("claim-surface")
		claimVerifiers, _ := cmd.Flags().GetStringArray("claim-verifier")
		claimContracts, _ := cmd.Flags().GetStringArray("claim-contract")
		claimSpecs, err := verificationClaimSpecs(claims, claimIDs, claimSurfaces, claimVerifiers, claimContracts)
		if err != nil {
			return err
		}
		specFlags, _ := cmd.Flags().GetStringArray("claim-spec")
		for _, spec := range specFlags {
			claim, perr := parseClaimSpec(spec)
			if perr != nil {
				return perr
			}
			claimSpecs = append(claimSpecs, claim)
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
	verifyCmd.Flags().StringArray("claim-id", nil, "optional stable ID for each --claim; required to prove a registered criterion")
	verifyCmd.Flags().StringArray("claim-surface", nil, "explicit surface for each --claim: code, browser, terminal, artifact, or secret")
	verifyCmd.Flags().StringArray("claim-verifier", nil, "optional exact verifier for each --claim (repeat once per claim)")
	verifyCmd.Flags().StringArray("claim-contract", nil, "required exact spec/check for each typed --claim (repeat once per claim)")
	verifyCmd.Flags().StringArray("claim-spec", nil, "one self-contained typed claim: id=|surface=|verifier=|contract=|<statement> (repeatable; replaces the coupled --claim-* flags)")
	verifyCmd.Flags().StringArray("changed-file", nil, "override changed files (repeatable; derived from git when omitted)")
	verifyCmd.Flags().String("browser-spec", "", "cairntrace spec path to prove browser claims")
	verifyCmd.Flags().String("terminal-spec", "", "glyphrun spec path to prove terminal claims")
	verifyCmd.Flags().String("artifact-ref", "", "fcheap stash ID or fcheap:// URI to prove an artifact claim")
	verifyCmd.Flags().String("secret-project", "", "tvault project whose value-free availability proves secret capability")
	verifyCmd.Flags().Bool("no-auto-specs", false, "don't auto-select and run the specs that cover the change when none is supplied")
	verifyCmd.Flags().Bool("no-op", false, "explicitly acknowledge that this change task intentionally produced no diff")
	verifyCmd.Flags().String("actor", "", "change-lease owner (defaults to the active lease owner when the task is leased)")
	rootCmd.AddCommand(verifyCmd)
}

func verificationClaimSpecs(claims, ids, surfaces, verifiers, contracts []string) ([]domain.VerificationClaim, error) {
	if len(ids) == 0 && len(surfaces) == 0 && len(verifiers) == 0 && len(contracts) == 0 {
		return nil, nil
	}
	if len(claims) == 0 || len(surfaces) != len(claims) {
		return nil, fmt.Errorf("--claim-surface must be repeated once for every --claim")
	}
	if len(verifiers) != 0 && len(verifiers) != len(claims) {
		return nil, fmt.Errorf("--claim-verifier must be omitted or repeated once for every --claim")
	}
	if len(ids) != 0 && len(ids) != len(claims) {
		return nil, fmt.Errorf("--claim-id must be omitted or repeated once for every --claim")
	}
	if len(contracts) != len(claims) {
		return nil, fmt.Errorf("--claim-contract must be repeated once for every typed --claim")
	}
	out := make([]domain.VerificationClaim, 0, len(claims))
	for i, statement := range claims {
		claim := domain.VerificationClaim{Statement: statement, Surface: domain.Surface(surfaces[i]), Required: true}
		if len(ids) > 0 {
			claim.ID = ids[i]
		}
		if len(verifiers) > 0 {
			claim.Verifier = verifiers[i]
		}
		claim.Contract = contracts[i]
		out = append(out, claim)
	}
	return out, nil
}

// parseClaimSpec parses one self-contained typed claim of the form
// "id=X|surface=Y|verifier=Z|contract=W|<statement>". Recognized key=value
// segments (id, surface, verifier, contract) set the matching field; every other
// pipe-separated segment is statement text, re-joined with "|" so a statement
// may itself contain pipes or "=". This is the ergonomic alternative to the
// coupled --claim-id/-surface/-verifier/-contract flags, which must be repeated
// in lockstep per claim. Surface/verifier validity is checked by the kernel.
func parseClaimSpec(spec string) (domain.VerificationClaim, error) {
	var claim domain.VerificationClaim
	var statement []string
	for _, part := range strings.Split(spec, "|") {
		key, value, isKV := strings.Cut(part, "=")
		if isKV {
			switch strings.TrimSpace(key) {
			case "id":
				claim.ID = strings.TrimSpace(value)
				continue
			case "surface":
				claim.Surface = domain.Surface(strings.TrimSpace(value))
				continue
			case "verifier":
				claim.Verifier = strings.TrimSpace(value)
				continue
			case "contract":
				claim.Contract = strings.TrimSpace(value)
				continue
			}
		}
		statement = append(statement, part)
	}
	claim.Statement = strings.TrimSpace(strings.Join(statement, "|"))
	claim.Required = true
	if claim.Statement == "" {
		return claim, fmt.Errorf("claim spec %q has no statement", spec)
	}
	return claim, nil
}
