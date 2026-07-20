package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// verification threads the mutable state Verify's stages share, so each stage is
// a focused method instead of one long function. The staged evidence/receipts
// are held back from the case until Verify's commit step publishes them as one
// revision-bound bundle; the per-surface/verifier/contract status maps are what
// named claims map against to decide pass/not_run.
type verification struct {
	k      *Kernel
	ctx    context.Context
	c      *domain.CaseFile
	in     VerifyInput
	claims []domain.VerificationClaim

	changed []string
	scope   ScopeReport

	stage    verificationStage
	facts    []domain.Evidence
	warnings []string

	surfaceStatus  map[domain.Surface]domain.VerificationStatus
	verifierStatus map[string]domain.VerificationStatus
	contractStatus map[string]domain.VerificationStatus

	claimStatuses      []domain.VerificationStatus
	pendingReceipts    []domain.VerificationRecord
	pendingAnnotations []behaviorAnnotation

	revision        adapters.Revision
	revisionWarning string
	batchID         string
	receiptActor    string
}

func (v *verification) writeReceipt(spec receiptSpec) error {
	spec.Actor = v.receiptActor
	v.pendingReceipts = append(v.pendingReceipts, v.k.makeReceipt(v.c.ID, v.batchID, v.revision, spec))
	return nil
}

func (v *verification) warn(msg string) { v.warnings = append(v.warnings, msg) }

// runStructuralReview runs the codemap diff review for a change with a diff. A
// review passes only when codemap is indexed and the diff is not rated high
// risk; an unindexed or high-risk review is inconclusive, never a clean pass.
func (v *verification) runStructuralReview() {
	if len(v.changed) == 0 {
		return
	}
	res := v.k.run(v.ctx, "codemap", adapters.Request{TaskID: v.c.ID, Operation: "review", Input: map[string]any{"since": v.c.Workspace.BaseRef}})
	v.warnings = append(v.warnings, res.Warnings...)
	evs := v.stage.stampAll(v.k, res, &v.facts)
	st := reviewStatus(res.Status)
	reviewNote := ""
	if st == domain.VerifyInconclusive {
		reviewNote = "codemap not indexed — structural review has no blast radius or test selection"
	}
	// A review that RAN authoritatively but that codemap rated HIGH risk is not
	// a clean structural pass — "the review ran on an indexed repo" must not be
	// conflated with "the diff passed review" (review 2026-07-07). Downgrade the
	// verdict to inconclusive so a high-risk diff can't satisfy the completion
	// gate on the review alone; the risk factors are already in the warnings.
	if st == domain.VerifyPassed && containsMarker(res.Warnings, "diff risk: high") {
		st = domain.VerifyInconclusive
		reviewNote = "codemap rated this diff HIGH risk — structural review is inconclusive, not a clean pass; address the risk factors or prove the change behaviorally (browser/terminal spec)"
	}
	v.surfaceStatus[domain.SurfaceCode] = st
	mergeVerificationStatus(v.verifierStatus, "codemap", st)
	mergeVerificationStatus(v.contractStatus, verificationTarget("codemap", "codemap_review"), st)
	if err := v.writeReceipt(receiptSpec{Claim: "structural review of the diff", Surface: domain.SurfaceCode,
		Purpose:     domain.VerificationPurposeVerifierRun,
		Requirement: "codemap_review", Tool: "codemap", Version: v.k.toolVersion(v.ctx, "codemap"), Status: st, Evidence: evs, Notes: reviewNote}); err != nil {
		v.warn("could not persist review receipt: " + err.Error())
	}
}

// enforceChangeControlRigor warns when a medium/high-risk change lacks a passing
// structural review.
func (v *verification) enforceChangeControlRigor() {
	if v.c.Mode == domain.ModeChange && len(v.changed) > 0 && (v.c.Risk == "medium" || v.c.Risk == "high") {
		if st := v.surfaceStatus[domain.SurfaceCode]; st != domain.VerifyPassed {
			v.warn(fmt.Sprintf("%s-risk change requires a structural diff review that passed, but codemap review is %s — run `codemap index` and re-verify",
				v.c.Risk, reviewStateWord(st)))
		}
	}
}

// runCommandVerifiers runs the repository-configured command verifiers named in
// the plan (cortex.yaml). Callers never provide executable text; planning
// resolves the names.
func (v *verification) runCommandVerifiers() {
	for _, requirement := range v.c.VerificationRequired {
		if !strings.HasPrefix(requirement, "command:") {
			continue
		}
		name := strings.TrimPrefix(requirement, "command:")
		res := v.k.run(v.ctx, "command", adapters.Request{
			TaskID: v.c.ID, Operation: name, Input: map[string]any{"dir": v.k.cfg.Workspace},
		})
		v.warnings = append(v.warnings, res.Warnings...)
		evs := v.stage.stampAll(v.k, res, &v.facts)
		st := commandVerificationStatus(res)
		v.surfaceStatus[domain.SurfaceCode] = worseStatus(v.surfaceStatus[domain.SurfaceCode], st)
		verifier := "command:" + name
		mergeVerificationStatus(v.verifierStatus, verifier, st)
		mergeVerificationStatus(v.contractStatus, verificationTarget(verifier, name), st)
		mergeVerificationStatus(v.contractStatus, verificationTarget(verifier, requirement), st)
		if err := v.writeReceipt(receiptSpec{
			Claim: "configured command verifier " + name, Surface: domain.SurfaceCode,
			Purpose: domain.VerificationPurposeVerifierRun, Requirement: requirement,
			Tool: "command", Status: st, Evidence: evs, Notes: commandLimitation(res, st),
		}); err != nil {
			v.warn("could not persist configured command receipt: " + err.Error())
		}
	}
}

// runBehavioralVerifiers runs browser/terminal specs. An explicit spec wins;
// otherwise, when the surface is in scope and a diff exists, cortex auto-selects
// the specs whose coverage intersects the change. A failed run is stashed to
// fcheap and the receipt links the durable stash.
func (v *verification) runBehavioralVerifiers() {
	for _, bs := range behavioralSurfaces {
		explicit := bs.specOf(v.in)
		var specs []string
		auto := false
		switch {
		case explicit != "":
			specs = []string{explicit}
		case !v.in.DisableAutoSpecs && surfaceInScope(v.c, bs.surface) && len(v.changed) > 0:
			auto = true
			specs = v.k.selectSpecs(v.ctx, v.c, bs.surface)
			if len(specs) > maxAutoSpecs {
				v.warn(fmt.Sprintf("%d %s specs cover this change; running the first %d", len(specs), bs.surface, maxAutoSpecs))
				specs = specs[:maxAutoSpecs]
			}
			if len(specs) == 0 {
				v.warn(fmt.Sprintf("no %s spec covers this change (auto-selection found none); %s claims stay unverified — supply a spec or add coverage", bs.surface, bs.surface))
			}
		}
		for _, spec := range specs {
			res := v.k.run(v.ctx, bs.tool, adapters.Request{TaskID: v.c.ID, Operation: "run", Input: map[string]any{"spec": spec}})
			v.warnings = append(v.warnings, res.Warnings...)
			evs := v.stage.stampAll(v.k, res, &v.facts)
			st := behavioralStatus(res)
			artifact, w := v.k.stashRunBundle(v.ctx, v.c, res, st == domain.VerifyPassed, string(bs.surface))
			v.warnings = append(v.warnings, w...)
			// The strongest evidence wins per surface: a pass on any covering spec
			// proves it, but a failure on any covering spec must not be masked.
			v.surfaceStatus[bs.surface] = worseStatus(v.surfaceStatus[bs.surface], st)
			mergeVerificationStatus(v.verifierStatus, bs.tool, st)
			mergeVerificationStatus(v.contractStatus, verificationTarget(bs.tool, spec), st)
			label := string(bs.surface) + " flow "
			if auto {
				label = "auto-selected " + label
			}
			if err := v.writeReceipt(receiptSpec{Claim: label + spec, Surface: bs.surface,
				Purpose:     domain.VerificationPurposeVerifierRun,
				Requirement: behavioralRequirement(bs.surface), Tool: bs.tool, Version: v.k.toolVersion(v.ctx, bs.tool), Status: st, Evidence: evs,
				Artifact: artifact, Notes: behavioralLimitation(res, st)}); err != nil {
				v.warn("could not persist " + string(bs.surface) + " receipt: " + err.Error())
			}
			v.pendingAnnotations = append(v.pendingAnnotations, behaviorAnnotation{tool: bs.tool, spec: spec, status: st, artifact: artifact})
		}
	}
}

// runCapabilityVerifiers runs the explicit artifact (fcheap) and secret-capability
// (tvault) verifiers. Neither silently falls through to structural code
// verification.
func (v *verification) runCapabilityVerifiers() {
	for _, sv := range []struct {
		surface                           domain.Surface
		tool, operation, key, requirement string
		value                             string
	}{
		{domain.SurfaceArtifact, "fcheap", "verify", "stash", "fcheap_artifact", v.in.ArtifactRef},
		{domain.SurfaceSecret, "tvault", "availability", "project", "tvault_capability", v.in.SecretProject},
	} {
		if !surfaceInScope(v.c, sv.surface) && sv.value == "" {
			continue
		}
		if sv.value == "" {
			v.surfaceStatus[sv.surface] = domain.VerifyNotRun
			v.warn(fmt.Sprintf("%s verification needs %s input; claims on this surface stay unverified", sv.surface, sv.key))
			continue
		}
		res := v.k.run(v.ctx, sv.tool, adapters.Request{TaskID: v.c.ID, Operation: sv.operation, Input: map[string]any{sv.key: sv.value}})
		v.warnings = append(v.warnings, res.Warnings...)
		evs := v.stage.stampAll(v.k, res, &v.facts)
		st := capabilityStatus(res)
		v.surfaceStatus[sv.surface] = st
		mergeVerificationStatus(v.verifierStatus, sv.tool, st)
		mergeVerificationStatus(v.contractStatus, verificationTarget(sv.tool, sv.value), st)
		if err := v.writeReceipt(receiptSpec{Claim: fmt.Sprintf("%s verification %s", sv.surface, sv.value), Surface: sv.surface,
			Purpose:     domain.VerificationPurposeVerifierRun,
			Requirement: sv.requirement, Tool: sv.tool, Version: v.k.toolVersion(v.ctx, sv.tool), Status: st, Evidence: evs,
			Artifact: firstArtifactURI(res), Notes: capabilityLimitation(sv.surface, st)}); err != nil {
			v.warn("could not persist " + string(sv.surface) + " receipt: " + err.Error())
		}
	}
}

// mapClaims maps each named claim to a verifier receipt and records the
// structured status per claim — never derived from strings that embed the
// free-text claim (a claim mentioning "passed" must not be counted as verified).
func (v *verification) mapClaims() {
	for _, claim := range v.claims {
		surf := claim.Surface
		verifier := claim.Verifier
		var st domain.VerificationStatus
		var ran bool
		if claim.Contract != "" {
			st, ran = v.contractStatus[verificationTarget(verifier, claim.Contract)]
		} else {
			st, ran = v.verifierStatus[verifier]
		}
		if !ran {
			st = domain.VerifyNotRun
			target := verifier
			if claim.Contract != "" {
				target += " contract " + claim.Contract
			}
			v.warn(fmt.Sprintf("claim %q needs %s, which was not run", clipStr(claim.Statement, 50), target))
		}
		if err := v.writeReceipt(receiptSpec{Claim: claim.Statement, ClaimID: claim.ID, Surface: surf,
			Purpose: domain.VerificationPurposeNamedClaim, Tool: verifier, Contract: claim.Contract,
			Status: st, Notes: claimLimitation(st)}); err != nil {
			v.warn("could not persist claim receipt: " + err.Error())
		}
		v.claimStatuses = append(v.claimStatuses, st)
	}
}

// recordScopeDriftEvidence stamps a scope-drift finding as evidence and a
// warning. Scope drift is a warning, not a failure.
func (v *verification) recordScopeDriftEvidence() {
	if v.scope.Scope == "drift_detected" {
		v.warn(fmt.Sprintf("scope drift (%s risk): %s changed outside the boundary — %s",
			v.scope.Risk, pluralizeGeneric(len(v.scope.UnexpectedFiles), "file", "files"), v.scope.Action))
		ev := v.stage.stampFact(v.k, "git", adapters.Fact{Kind: "code_location", Confidence: "high",
			Claim: "scope drift: changed outside declared boundary: " + strings.Join(v.scope.UnexpectedFiles, ", ")})
		v.facts = append(v.facts, ev)
	}
}
