package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/forge"
)

// ReviewInput parameterizes Review (an evidence-backed branch/PR review).
type ReviewInput struct {
	Base     string           // base ref to diff from; empty → merge-base with the default branch
	Head     string           // ref to review; empty → current branch (HEAD)
	PR       int              // pull/merge request number to fetch+review; 0 → local branch
	Surfaces []domain.Surface // surfaces to check; empty → code
	Risk     string           // low|medium|high; empty → medium
	Claims   []string         // extra user-facing claims to verify; empty → derived
}

// Review performs a diff-scoped, evidence-backed review of a branch or PR. It
// resolves the diff (host-agnostic for a branch; via the forge for a PR),
// creates a ModeReview case, gathers structural + semantic context, runs the
// verifiers over base…HEAD (structural review + auto-selected behavioral specs),
// and completes with a verdict whose every claim is backed by a receipt.
func (k *Kernel) Review(ctx context.Context, in ReviewInput) (out domain.Envelope, retErr error) {
	surfaces, err := normalizeSurfaces(in.Surfaces)
	if err != nil {
		return errEnvelope("", err.Error()), nil
	}
	risk, ok := normalizeRisk(in.Risk)
	if !ok {
		return errEnvelope("", fmt.Sprintf("risk must be one of: low, medium, high (got %q)", in.Risk)), nil
	}
	if in.PR < 0 {
		return errEnvelope("", "review pr number cannot be negative"), nil
	}
	if textExceeds(strings.TrimSpace(in.Base), maxLocatorBytes) || textExceeds(strings.TrimSpace(in.Head), maxLocatorBytes) {
		return errEnvelope("", fmt.Sprintf("review refs must be at most %d bytes", maxLocatorBytes)), nil
	}
	if len(in.Claims) > 128 {
		return errEnvelope("", "review accepts at most 128 claims"), nil
	}
	for _, claim := range in.Claims {
		if textExceeds(strings.TrimSpace(claim), maxRecordTextBytes) {
			return errEnvelope("", fmt.Sprintf("review claim exceeds %d bytes", maxRecordTextBytes)), nil
		}
	}
	in.Surfaces, in.Risk = surfaces, risk
	if k.git == nil {
		return errEnvelope("", "review needs a git workspace"), nil
	}
	base, head, restore, warns, err := k.resolveReviewScope(ctx, in)
	if err != nil {
		return errEnvelope("", err.Error()), nil
	}
	// Leave the working tree as we found it when we checked out a different ref.
	if restore != "" {
		defer func() {
			if err := k.git.Checkout(context.Background(), k.cfg.Workspace, restore); err != nil {
				message := k.red.String(fmt.Sprintf("review finished but could not restore workspace to %q: %v", restore, err))
				priorSummary := out.Summary
				out.OK = false
				out.Summary = message
				out.Error = message
				out.Warnings = append(out.Warnings, message)
				if priorSummary != "" {
					out.Warnings = append(out.Warnings, "review result before restoration failure: "+priorSummary)
				}
				out.Warnings = dedupeStr(out.Warnings)
			}
		}()
	}
	// The review diffs base…HEAD (HEAD is now the resolved head). A bad base ref
	// is a hard error, not a false "no changes".
	changed, cerr := k.git.ChangedFiles(ctx, k.cfg.Workspace, base, false)
	if cerr != nil {
		return errEnvelope("", "could not compute the review diff: "+cerr.Error()), nil
	}
	if len(changed) == 0 {
		return domain.Envelope{OK: true, Summary: k.red.String(fmt.Sprintf("no changes to review between %s and %s", clipStr(base, 12), head))}, nil
	}

	goal := fmt.Sprintf("review %s (%s changed)", head, pluralizeGeneric(len(changed), "file", "files"))

	start, err := k.StartTask(ctx, StartInput{Goal: goal, Mode: domain.ModeReview, Surfaces: surfaces, Risk: risk, BaseRef: base})
	if err != nil || !start.OK {
		return start, err
	}
	id := start.TaskID
	warns = append(warns, start.Warnings...)
	degraded := start.Degraded

	// Gather structural + semantic context for the diff (also recalls prior
	// memories on the touched code).
	investigated, investigateErr := k.Investigate(ctx, InvestigateInput{TaskID: id, Question: "review the changes to " + strings.Join(clipList(changed, 5), ", "), Surfaces: surfaces})
	warns = append(warns, investigated.Warnings...)
	degraded = degraded || investigated.Degraded
	if investigateErr != nil || !investigated.OK {
		investigated.Warnings = dedupeStr(warns)
		investigated.Degraded = degraded
		return investigated, investigateErr
	}

	// A review's "plan" frames what must hold: it is falsifiable by the very
	// verifiers cortex will run. The boundary is the diff's file set.
	plan, planErr := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: fmt.Sprintf("the change on %s is correct and adequately verified", head), Confidence: "medium", DisproveBy: "structural review of the diff plus the behavioral specs that cover it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: changed, Reason: "the reviewed diff"},
		Verification:   reviewVerifiers(surfaces),
		Uncertainty:    fmt.Sprintf("reviewing %s across %s", pluralizeGeneric(len(changed), "file", "files"), joinSurfaces(surfaces))})
	warns = append(warns, plan.Warnings...)
	degraded = degraded || plan.Degraded
	if planErr != nil || !plan.OK {
		plan.Warnings = dedupeStr(warns)
		plan.Degraded = degraded
		return plan, planErr
	}

	// Verify the diff (base-scoped): structural review + auto-selected specs. The
	// derived per-surface claims are ALWAYS included (a --claim augments, never
	// replaces) so each declared surface keeps a claim that forces an honest
	// not_run receipt when its verifier didn't run.
	claims := dedupeStr(append(reviewClaims(surfaces), in.Claims...))
	vr, verifyErr := k.Verify(ctx, VerifyInput{TaskID: id, Claims: claims, ChangedFiles: changed})
	warns = append(warns, vr.Warnings...)
	degraded = degraded || vr.Degraded
	if verifyErr != nil || !vr.OK {
		vr.Warnings = dedupeStr(warns)
		vr.Degraded = degraded
		return vr, verifyErr
	}

	// Derive the verdict from the receipts AGAINST the required-verifier set, so
	// APPROVE can't fire while a declared surface's verifier never ran.
	c2, loadErr := k.store.Load(id)
	if loadErr != nil {
		failure := errEnvelope(id, loadErr.Error())
		failure.Warnings, failure.Degraded = dedupeStr(warns), degraded
		return failure, nil
	}
	receipts, receiptsErr := k.store.Verifications(id)
	if receiptsErr != nil {
		failure := errEnvelope(id, receiptsErr.Error())
		failure.Warnings, failure.Degraded = dedupeStr(warns), degraded
		return failure, nil
	}
	assessment := assessCaseVerification(c2, receipts)
	verdict, _ := reviewVerdict(receipts, c2.VerificationRequired)
	outcome := k.red.String(fmt.Sprintf("REVIEW %s: %s (%s changed, base %s)", verdict, head, pluralizeGeneric(len(changed), "file", "files"), clipStr(base, 12)))
	// Partial/unverified reviews need the explicit incomplete-verification
	// acknowledgement. A REQUEST CHANGES verdict rests on a verifier that ran and
	// FAILED, so it uses accept_failed instead.
	notPossible := assessment.Outcome == VerificationPartial || assessment.Outcome == VerificationUnverified
	// A REQUEST CHANGES review rests on failed verdicts — accept those so the case
	// can complete with an honest failed outcome. Mixed pass+fail is still failed.
	acceptFailed := assessment.Outcome == VerificationFailed
	rem, rememberErr := k.Remember(ctx, RememberInput{
		TaskID: id, Outcome: outcome, Tags: []string{"review"},
		VerificationNotPossible: notPossible, AcceptFailed: acceptFailed,
	})
	warns = append(warns, rem.Warnings...)
	degraded = degraded || rem.Degraded
	if rememberErr != nil || !rem.OK {
		rem.Warnings = dedupeStr(warns)
		rem.Degraded = degraded
		return rem, rememberErr
	}

	env := rem
	env.Summary = outcome
	env.Warnings = dedupeStr(warns)
	env.Degraded = degraded
	env.NextActions = append([]string{
		"read the evidence-backed review: cortex status " + id + " --detail full",
	}, rem.NextActions...)
	return env, nil
}

// resolveReviewScope resolves the diff scope. The review always assesses HEAD, so
// a PR or an explicit --head is checked out first (guarded against a dirty tree,
// and restored afterwards) — otherwise the diff-scoped tools would run against
// whatever happens to be checked out, not the ref the user asked to review. The
// base is the fork point (merge-base) so the diff is exactly base…HEAD.
func (k *Kernel) resolveReviewScope(ctx context.Context, in ReviewInput) (base, head, restore string, warns []string, err error) {
	ws := k.cfg.Workspace
	original, _ := k.git.CurrentBranch(ctx, ws)

	target := "" // ref to check out; "" = review the current HEAD in place
	if in.PR > 0 {
		remoteURL, _ := k.git.RemoteURL(ctx, ws, "")
		fg := forge.Detect(remoteURL)
		branch := fmt.Sprintf("cortex/pr-%d", in.PR)
		if ferr := k.git.FetchRef(ctx, ws, "", fg.PRHeadRefspecs(in.PR, "refs/heads/"+branch)...); ferr != nil {
			return "", "", "", nil, fmt.Errorf("%s", fg.FetchHint(in.PR))
		}
		target = branch
		warns = append(warns, fmt.Sprintf("fetched %s #%d as %s", fg.PRTerm(), in.PR, branch))
	} else if in.Head != "" && in.Head != original {
		target = in.Head
	}

	if target != "" {
		// Checking out a different ref mutates the working tree — refuse on a dirty
		// tree so uncommitted work is never clobbered.
		if info, e := k.git.Status(ctx, ws); e == nil && info.Dirty {
			return "", "", "", nil, fmt.Errorf("working tree is dirty — commit or stash before reviewing %q", target)
		}
		if cerr := k.git.Checkout(ctx, ws, target); cerr != nil {
			return "", "", "", nil, fmt.Errorf("could not check out %q: %v", target, cerr)
		}
		if original != "" && original != "HEAD" {
			restore = original
		}
	}

	head, _ = k.git.CurrentBranch(ctx, ws)
	if head == "" || head == "HEAD" {
		head = firstNonEmptyStr(target, "HEAD")
	}
	baseBranch := in.Base
	if baseBranch == "" {
		baseBranch = k.git.DefaultBranch(ctx, ws, "")
	}
	if mb, e := k.git.MergeBase(ctx, ws, baseBranch, "HEAD"); e == nil && mb != "" {
		base = mb
	} else {
		base = baseBranch
	}
	return base, head, restore, warns, nil
}

// reviewVerifiers is the required-verifier set for a review's declared surfaces.
func reviewVerifiers(surfaces []domain.Surface) []string {
	v := []string{"codemap_review"}
	for _, s := range surfaces {
		switch s {
		case domain.SurfaceBrowser:
			v = append(v, "cairntrace_flow")
		case domain.SurfaceTerminal:
			v = append(v, "glyphrun_flow")
		}
	}
	return v
}

// reviewClaims is the default set of user-facing claims a review proves.
func reviewClaims(surfaces []domain.Surface) []string {
	claims := []string{"the changed code is structurally sound and its blast radius is covered by tests"}
	for _, s := range surfaces {
		switch s {
		case domain.SurfaceBrowser:
			claims = append(claims, "the change's browser behavior is unbroken")
		case domain.SurfaceTerminal:
			claims = append(claims, "the change's terminal behavior is unbroken")
		}
	}
	return claims
}

// reviewVerdict turns the receipts into a verdict, gated on the required-verifier
// set: any failure → request changes; every required verifier passed AND
// something actually passed → approve; otherwise → needs verification (a required
// verifier that never ran must never read as approved.
func reviewVerdict(receipts []domain.VerificationRecord, required []string) (verdict string, fullyVerified bool) {
	assessment := assessVerification(required, receipts)
	switch assessment.Outcome {
	case VerificationFailed:
		return "REQUEST CHANGES", false
	case VerificationVerified:
		return "APPROVE", true
	default:
		return "NEEDS VERIFICATION", false
	}
}

func clipList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	out := append([]string{}, xs[:n]...)
	return append(out, fmt.Sprintf("…+%d more", len(xs)-n))
}

func joinSurfaces(ss []domain.Surface) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, string(s))
	}
	return strings.Join(out, "+")
}
