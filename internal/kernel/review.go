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
func (k *Kernel) Review(ctx context.Context, in ReviewInput) (domain.Envelope, error) {
	if k.git == nil {
		return errEnvelope("", "review needs a git workspace"), nil
	}
	base, head, restore, warns, err := k.resolveReviewScope(ctx, in)
	if err != nil {
		return errEnvelope("", err.Error()), nil
	}
	// Leave the working tree as we found it when we checked out a different ref.
	if restore != "" {
		defer func() { _ = k.git.Checkout(context.Background(), k.cfg.Workspace, restore) }()
	}
	// The review diffs base…HEAD (HEAD is now the resolved head). A bad base ref
	// is a hard error, not a false "no changes".
	changed, cerr := k.git.ChangedFiles(ctx, k.cfg.Workspace, base, false)
	if cerr != nil {
		return errEnvelope("", "could not compute the review diff: "+cerr.Error()), nil
	}
	if len(changed) == 0 {
		return domain.Envelope{OK: true, Summary: fmt.Sprintf("no changes to review between %s and %s", clipStr(base, 12), head)}, nil
	}

	surfaces := in.Surfaces
	if len(surfaces) == 0 {
		surfaces = []domain.Surface{domain.SurfaceCode}
	}
	risk := in.Risk
	if risk == "" {
		risk = "medium"
	}
	goal := fmt.Sprintf("review %s (%s changed)", head, pluralizeGeneric(len(changed), "file", "files"))

	start, err := k.StartTask(ctx, StartInput{Goal: goal, Mode: domain.ModeReview, Surfaces: surfaces, Risk: risk, BaseRef: base})
	if err != nil || !start.OK {
		return start, err
	}
	id := start.TaskID
	warns = append(warns, start.Warnings...)

	// Gather structural + semantic context for the diff (also recalls prior
	// memories on the touched code).
	_, _ = k.Investigate(ctx, InvestigateInput{TaskID: id, Question: "review the changes to " + strings.Join(clipList(changed, 5), ", "), Surfaces: surfaces})

	// A review's "plan" frames what must hold: it is falsifiable by the very
	// verifiers cortex will run. The boundary is the diff's file set.
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: fmt.Sprintf("the change on %s is correct and adequately verified", head), Confidence: "medium", DisproveBy: "structural review of the diff plus the behavioral specs that cover it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: changed, Reason: "the reviewed diff"},
		Verification:   reviewVerifiers(surfaces),
		Uncertainty:    fmt.Sprintf("reviewing %s across %s", pluralizeGeneric(len(changed), "file", "files"), joinSurfaces(surfaces))})
	if !plan.OK {
		return plan, nil
	}

	// Verify the diff (base-scoped): structural review + auto-selected specs. The
	// derived per-surface claims are ALWAYS included (a --claim augments, never
	// replaces) so each declared surface keeps a claim that forces an honest
	// not_run receipt when its verifier didn't run.
	claims := dedupeStr(append(reviewClaims(surfaces), in.Claims...))
	vr, _ := k.Verify(ctx, VerifyInput{TaskID: id, Claims: claims, ChangedFiles: changed})
	warns = append(warns, vr.Warnings...)

	// Derive the verdict from the receipts AGAINST the required-verifier set, so
	// APPROVE can't fire while a declared surface's verifier never ran (SPEC §14.2).
	c2, _ := k.store.Load(id)
	receipts, _ := k.store.Verifications(id)
	verdict, _ := reviewVerdict(receipts, c2.VerificationRequired)
	outcome := fmt.Sprintf("REVIEW %s: %s (%s changed, base %s)", verdict, head, pluralizeGeneric(len(changed), "file", "files"), clipStr(base, 12))
	// verification_not_possible means "no verifier could run" — set it ONLY when
	// no definitive verdict exists (e.g. codemap unindexed, no covering specs),
	// NOT merely because the review wasn't fully green. A REQUEST CHANGES verdict
	// rests on a verifier that ran and FAILED — that IS a real verification, so
	// claiming it was "not possible" would be dishonest and would also skip the
	// completion gate's failed-verification warning (review 2026-07-07).
	notPossible := !hasDefinitiveVerification(receipts)
	// A REQUEST CHANGES review rests on failed verdicts — accept those so the
	// case can complete with an honest failed outcome (not "verification not
	// possible"). Passes complete normally; empty/inconclusive uses notPossible.
	acceptFailed := hasFailedVerification(receipts) && !hasPassingVerification(receipts)
	rem, _ := k.Remember(ctx, RememberInput{
		TaskID: id, Outcome: outcome, Tags: []string{"review"},
		VerificationNotPossible: notPossible, AcceptFailed: acceptFailed,
	})

	env := rem
	env.Summary = outcome
	env.Warnings = dedupeStr(append(warns, rem.Warnings...))
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
// verifier that never ran must never read as approved — SPEC §14.2).
func reviewVerdict(receipts []domain.VerificationRecord, required []string) (verdict string, fullyVerified bool) {
	anyFailed, anyPassed := false, false
	for _, r := range receipts {
		switch r.Status {
		case domain.VerifyFailed:
			anyFailed = true
		case domain.VerifyPassed:
			anyPassed = true
		}
	}
	allRequiredMet := true
	for _, req := range required {
		if !verifierSatisfied(req, receipts) {
			allRequiredMet = false
			break
		}
	}
	switch {
	case anyFailed:
		return "REQUEST CHANGES", false
	case anyPassed && allRequiredMet:
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
