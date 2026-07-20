package kernel

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// VerifyInput parameterizes Verify.
type VerifyInput struct {
	TaskID        string
	Actor         string
	Claims        []string
	ClaimSpecs    []domain.VerificationClaim
	ChangedFiles  []string // optional; derived from git when empty
	BrowserSpec   string   // cairn spec path (proves browser claims)
	TerminalSpec  string   // glyph spec path (proves terminal claims)
	ArtifactRef   string   // fcheap stash ID/URI to verify exists and is readable
	SecretProject string   // tvault project whose value-free availability proves secret capability
	// DisableAutoSpecs turns OFF verify-time auto-selection of covering specs
	// (default on). Auto-selection runs only when no explicit spec is given for a
	// declared behavioral surface and a diff exists.
	DisableAutoSpecs bool
	// NoOpAcknowledged explicitly permits a change task with no diff/change
	// record to enter verification. This is kernel-only for now: callers must opt
	// in deliberately rather than a missing edit being treated as a successful
	// change lifecycle.
	NoOpAcknowledged bool
}

// behavioralSurfaces pairs each behavioral surface with its verifier tool and a
// selector for the explicitly-supplied spec.
var behavioralSurfaces = []struct {
	surface domain.Surface
	tool    string
	specOf  func(VerifyInput) string
}{
	{domain.SurfaceBrowser, "cairntrace", func(in VerifyInput) string { return in.BrowserSpec }},
	{domain.SurfaceTerminal, "glyphrun", func(in VerifyInput) string { return in.TerminalSpec }},
}

// maxAutoSpecs bounds how many auto-selected specs cortex runs per surface, so a
// broad change can't launch an unbounded number of browser/terminal runs.
const maxAutoSpecs = 3

// Verify runs the verification policy and reports whether the named claims are
// supported. It runs a structural diff review, executes any provided
// behavioral specs, checks for scope drift, and writes a receipt per claim. A
// claim with no relevant verifier is recorded not_run — never rendered as
// passed.
func (k *Kernel) Verify(ctx context.Context, in VerifyInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if c.Status != domain.PhasePlanned && c.Status != domain.PhaseChanging && c.Status != domain.PhaseVerifying {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot verify in phase %q; plan the task first", c.Status)), nil
	}
	if strings.TrimSpace(in.Actor) != "" {
		actor, actorErr := k.changeLeaseActor(in.Actor)
		if actorErr != nil {
			return errEnvelope(in.TaskID, actorErr.Error()), nil
		}
		in.Actor = actor
	} else if owner := activeLeaseActor(c, k.now().UTC()); owner != "" {
		// No actor supplied but the task has an active lease: default to the
		// lease owner, so a single actor who ran begin-change need not repeat
		// --actor at verify. An explicitly supplied actor is still checked
		// against the owner below; released/expired leases return "" here and
		// therefore still fail validation.
		in.Actor = owner
	}
	if err := validateVerificationLease(c, in.Actor, k.now().UTC()); err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	claims, claimErr := k.normalizeClaims(in.Claims, in.ClaimSpecs)
	if claimErr != nil {
		return errEnvelope(in.TaskID, k.red.String(claimErr.Error())), nil
	}
	if claimErr := validateRegisteredClaimIdentities(c.AcceptanceCriteria, claims); claimErr != nil {
		return errEnvelope(in.TaskID, k.red.String(claimErr.Error())), nil
	}
	if claimErr := k.validateStableClaimIdentities(c.ID, claims); claimErr != nil {
		return errEnvelope(in.TaskID, k.red.String(claimErr.Error())), nil
	}

	// Git is authoritative for changed files. Caller-provided files are additive
	// hints only, so an incomplete list cannot hide an out-of-boundary change.
	var gitChanged []string
	var warnings []string
	gitChangesKnown := false
	if k.git != nil {
		cf, cfErr := k.git.ChangedFiles(ctx, k.cfg.Workspace, c.Workspace.BaseRef, false)
		if cfErr != nil {
			warnings = append(warnings, "could not detect changed files via git: "+cfErr.Error()+" — scope drift may be incomplete; pass --changed-file to be precise")
		} else {
			gitChanged = cf
			gitChangesKnown = true
		}
	}
	changed := mergeChangedFiles(gitChanged, in.ChangedFiles)
	// Caller-provided paths are scope-analysis hints, not proof that a change
	// exists. When Git answered authoritatively, only its observed diff may pass
	// the no-op gate; otherwise a clean tree could be laundered into a change by
	// passing --changed-file for an untouched path.
	hasChangeRecord := len(changed) > 0
	if gitChangesKnown {
		hasChangeRecord = len(gitChanged) > 0
	}
	if c.Mode == domain.ModeChange && !hasChangeRecord && !in.NoOpAcknowledged {
		return errEnvelope(c.ID, "cannot verify change task: no diff/change record detected; make the planned change or explicitly acknowledge an intentional no-op"), nil
	}
	if c.Mode == domain.ModeChange && !hasChangeRecord {
		warnings = append(warnings, "no diff/change record detected — intentional no-op explicitly acknowledged")
	}
	scope := k.detectScopeDrift(ctx, c, changed)

	var facts []domain.Evidence
	stage := verificationStage{taskID: c.ID}
	// surfaceStatus records each surface's verifier outcome this run. Surfaces
	// with no entry were not verified at all.
	surfaceStatus := map[domain.Surface]domain.VerificationStatus{}
	verifierStatus := map[string]domain.VerificationStatus{}
	contractStatus := map[string]domain.VerificationStatus{}
	revision, revisionWarning := k.currentRevision(ctx)
	if revisionWarning != "" {
		warnings = append(warnings, revisionWarning)
	}
	verifyStarted := k.now().UTC()
	startCaseRevision := c.Revision
	startLease := cloneChangeLease(c.ChangeLease)
	startLeaseActive := startLease != nil && startLease.Active(verifyStarted)
	batchID := ids.New("vb")
	pendingReceipts := make([]domain.VerificationRecord, 0, len(c.VerificationRequired)+len(claims)+2)
	var pendingAnnotations []behaviorAnnotation
	receiptActor := firstNonEmptyStr(strings.TrimSpace(in.Actor), activeLeaseActor(c, k.now().UTC()), c.Actor)
	ctx = withCommandActor(ctx, receiptActor)
	writeReceipt := func(spec receiptSpec) error {
		spec.Actor = receiptActor
		pendingReceipts = append(pendingReceipts, k.makeReceipt(c.ID, batchID, revision, spec))
		return nil
	}

	// 1) Structural review (always, for a change task with a diff). A review is
	// passed only when codemap is indexed; an unindexed review is inconclusive.
	if len(changed) > 0 {
		res := k.run(ctx, "codemap", adapters.Request{TaskID: c.ID, Operation: "review", Input: map[string]any{"since": c.Workspace.BaseRef}})
		warnings = append(warnings, res.Warnings...)
		evs := stage.stampAll(k, res, &facts)
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
		surfaceStatus[domain.SurfaceCode] = st
		mergeVerificationStatus(verifierStatus, "codemap", st)
		mergeVerificationStatus(contractStatus, verificationTarget("codemap", "codemap_review"), st)
		if err := writeReceipt(receiptSpec{Claim: "structural review of the diff", Surface: domain.SurfaceCode,
			Purpose:     domain.VerificationPurposeVerifierRun,
			Requirement: "codemap_review", Tool: "codemap", Version: k.toolVersion(ctx, "codemap"), Status: st, Evidence: evs, Notes: reviewNote}); err != nil {
			warnings = append(warnings, "could not persist review receipt: "+err.Error())
		}
	}

	// Apply additional change-control rigor for change tasks.
	if c.Mode == domain.ModeChange {
		if len(changed) > 0 && (c.Risk == "medium" || c.Risk == "high") {
			// Medium/high-risk tasks must have a passing structural review.
			if st := surfaceStatus[domain.SurfaceCode]; st != domain.VerifyPassed {
				warnings = append(warnings, fmt.Sprintf("%s-risk change requires a structural diff review that passed, but codemap review is %s — run `codemap index` and re-verify",
					c.Risk, reviewStateWord(st)))
			}
		}
	}

	// 1b) Repository-configured command verifiers. Planning resolves names from
	// cortex.yaml; callers never provide executable text through MCP or the CLI.
	for _, requirement := range c.VerificationRequired {
		if !strings.HasPrefix(requirement, "command:") {
			continue
		}
		name := strings.TrimPrefix(requirement, "command:")
		res := k.run(ctx, "command", adapters.Request{
			TaskID: c.ID, Operation: name, Input: map[string]any{"dir": k.cfg.Workspace},
		})
		warnings = append(warnings, res.Warnings...)
		evs := stage.stampAll(k, res, &facts)
		st := commandVerificationStatus(res)
		surfaceStatus[domain.SurfaceCode] = worseStatus(surfaceStatus[domain.SurfaceCode], st)
		verifier := "command:" + name
		mergeVerificationStatus(verifierStatus, verifier, st)
		mergeVerificationStatus(contractStatus, verificationTarget(verifier, name), st)
		mergeVerificationStatus(contractStatus, verificationTarget(verifier, requirement), st)
		if err := writeReceipt(receiptSpec{
			Claim: "configured command verifier " + name, Surface: domain.SurfaceCode,
			Purpose: domain.VerificationPurposeVerifierRun, Requirement: requirement,
			Tool: "command", Status: st, Evidence: evs, Notes: commandLimitation(res, st),
		}); err != nil {
			warnings = append(warnings, "could not persist configured command receipt: "+err.Error())
		}
	}

	// 2) Behavioral verifiers. An explicit spec wins; otherwise, when the surface
	// is in the task's declared set and a diff exists, cortex auto-selects the
	// specs whose coverage intersects the change (cairn --select-only / glyph
	// affected-specs) and runs them — turning a not_run receipt into a real
	// verification instead of requiring the agent to name the check.
	// A failed run is stashed to fcheap and the receipt links the durable stash.
	for _, bs := range behavioralSurfaces {
		explicit := bs.specOf(in)
		var specs []string
		auto := false
		switch {
		case explicit != "":
			specs = []string{explicit}
		case !in.DisableAutoSpecs && surfaceInScope(c, bs.surface) && len(changed) > 0:
			auto = true
			specs = k.selectSpecs(ctx, c, bs.surface)
			if len(specs) > maxAutoSpecs {
				warnings = append(warnings, fmt.Sprintf("%d %s specs cover this change; running the first %d", len(specs), bs.surface, maxAutoSpecs))
				specs = specs[:maxAutoSpecs]
			}
			if len(specs) == 0 {
				warnings = append(warnings, fmt.Sprintf("no %s spec covers this change (auto-selection found none); %s claims stay unverified — supply a spec or add coverage", bs.surface, bs.surface))
			}
		}
		for _, spec := range specs {
			res := k.run(ctx, bs.tool, adapters.Request{TaskID: c.ID, Operation: "run", Input: map[string]any{"spec": spec}})
			warnings = append(warnings, res.Warnings...)
			evs := stage.stampAll(k, res, &facts)
			st := behavioralStatus(res)
			artifact, w := k.stashRunBundle(ctx, c, res, st == domain.VerifyPassed, string(bs.surface))
			warnings = append(warnings, w...)
			// The strongest evidence wins per surface: a pass on any covering spec
			// proves it, but a failure on any covering spec must not be masked.
			surfaceStatus[bs.surface] = worseStatus(surfaceStatus[bs.surface], st)
			mergeVerificationStatus(verifierStatus, bs.tool, st)
			mergeVerificationStatus(contractStatus, verificationTarget(bs.tool, spec), st)
			label := string(bs.surface) + " flow "
			if auto {
				label = "auto-selected " + label
			}
			if err := writeReceipt(receiptSpec{Claim: label + spec, Surface: bs.surface,
				Purpose:     domain.VerificationPurposeVerifierRun,
				Requirement: behavioralRequirement(bs.surface), Tool: bs.tool, Version: k.toolVersion(ctx, bs.tool), Status: st, Evidence: evs,
				Artifact: artifact, Notes: behavioralLimitation(res, st)}); err != nil {
				warnings = append(warnings, "could not persist "+string(bs.surface)+" receipt: "+err.Error())
			}
			pendingAnnotations = append(pendingAnnotations, behaviorAnnotation{tool: bs.tool, spec: spec, status: st, artifact: artifact})
		}
	}

	// 2b) Artifact and secret-capability verifiers. These are intentionally
	// explicit: a stash URI proves a durable artifact exists; a tvault project
	// proves value-free secret capability is available. Neither silently falls
	// through to structural code verification.
	for _, sv := range []struct {
		surface                           domain.Surface
		tool, operation, key, requirement string
		value                             string
	}{
		{domain.SurfaceArtifact, "fcheap", "verify", "stash", "fcheap_artifact", in.ArtifactRef},
		{domain.SurfaceSecret, "tvault", "availability", "project", "tvault_capability", in.SecretProject},
	} {
		if !surfaceInScope(c, sv.surface) && sv.value == "" {
			continue
		}
		if sv.value == "" {
			surfaceStatus[sv.surface] = domain.VerifyNotRun
			warnings = append(warnings, fmt.Sprintf("%s verification needs %s input; claims on this surface stay unverified", sv.surface, sv.key))
			continue
		}
		res := k.run(ctx, sv.tool, adapters.Request{TaskID: c.ID, Operation: sv.operation, Input: map[string]any{sv.key: sv.value}})
		warnings = append(warnings, res.Warnings...)
		evs := stage.stampAll(k, res, &facts)
		st := capabilityStatus(res)
		surfaceStatus[sv.surface] = st
		mergeVerificationStatus(verifierStatus, sv.tool, st)
		mergeVerificationStatus(contractStatus, verificationTarget(sv.tool, sv.value), st)
		if err := writeReceipt(receiptSpec{Claim: fmt.Sprintf("%s verification %s", sv.surface, sv.value), Surface: sv.surface,
			Purpose:     domain.VerificationPurposeVerifierRun,
			Requirement: sv.requirement, Tool: sv.tool, Version: k.toolVersion(ctx, sv.tool), Status: st, Evidence: evs,
			Artifact: firstArtifactURI(res), Notes: capabilityLimitation(sv.surface, st)}); err != nil {
			warnings = append(warnings, "could not persist "+string(sv.surface)+" receipt: "+err.Error())
		}
	}

	// 3) Map each named claim to a verifier receipt. Track the
	// structured status per claim — never derive the pass count from strings
	// that embed the free-text claim (a claim mentioning "passed" must not be
	// counted as verified).
	var claimStatuses []domain.VerificationStatus
	for _, claim := range claims {
		surf := claim.Surface
		verifier := claim.Verifier
		var st domain.VerificationStatus
		var ran bool
		if claim.Contract != "" {
			st, ran = contractStatus[verificationTarget(verifier, claim.Contract)]
		} else {
			st, ran = verifierStatus[verifier]
		}
		if !ran {
			st = domain.VerifyNotRun
			target := verifier
			if claim.Contract != "" {
				target += " contract " + claim.Contract
			}
			warnings = append(warnings, fmt.Sprintf("claim %q needs %s, which was not run", clipStr(claim.Statement, 50), target))
		}
		if err := writeReceipt(receiptSpec{Claim: claim.Statement, ClaimID: claim.ID, Surface: surf,
			Purpose: domain.VerificationPurposeNamedClaim, Tool: verifier, Contract: claim.Contract,
			Status: st, Notes: claimLimitation(st)}); err != nil {
			warnings = append(warnings, "could not persist claim receipt: "+err.Error())
		}
		claimStatuses = append(claimStatuses, st)
	}

	// Scope drift is a warning, not a failure.
	if scope.Scope == "drift_detected" {
		warnings = append(warnings, fmt.Sprintf("scope drift (%s risk): %s changed outside the boundary — %s",
			scope.Risk, pluralizeGeneric(len(scope.UnexpectedFiles), "file", "files"), scope.Action))
		ev := stage.stampFact(k, "git", adapters.Fact{Kind: "code_location", Confidence: "high",
			Claim: "scope drift: changed outside declared boundary: " + strings.Join(scope.UnexpectedFiles, ", ")})
		facts = append(facts, ev)
	}

	postRevision, postWarning := k.currentRevision(ctx)
	if postWarning != "" {
		warnings = append(warnings, "could not bind verifier completion to the workspace: "+postWarning)
	}
	committed, moves, err := k.prepareVerificationPhase(c.ID, in.Actor, startCaseRevision, startLease, startLeaseActive)
	if err != nil {
		return errEnvelope(c.ID, k.red.String(err.Error())), err
	}
	c = committed
	commitRevision, commitWarning := k.currentRevision(ctx)
	if commitWarning != "" {
		warnings = append(warnings, "could not recheck workspace state before committing verification: "+commitWarning)
	}
	binding := domain.VerificationBound
	bindReason := ""
	if revisionWarning != "" || postWarning != "" || commitWarning != "" ||
		!sameRevision(revision, postRevision) || !sameRevision(revision, commitRevision) {
		binding = domain.VerificationUnbound
		bindReason = "workspace revision/diff changed or could not be rechecked while verifiers ran"
	}
	if startLeaseActive && !sameLeaseIdentity(startLease, c.ChangeLease) {
		binding = domain.VerificationUnbound
		bindReason = "change ownership changed while verifiers ran"
	}
	if startLeaseActive && (c.ChangeLease == nil || !c.ChangeLease.Active(k.now().UTC())) {
		binding = domain.VerificationUnbound
		bindReason = "change lease expired while verifiers ran"
	}
	if binding == domain.VerificationUnbound {
		warnings = append(warnings, bindReason+"; definitive results were downgraded to inconclusive")
		for i := range pendingReceipts {
			pendingReceipts[i].Binding = binding
			if pendingReceipts[i].Status == domain.VerifyPassed || pendingReceipts[i].Status == domain.VerifyFailed {
				pendingReceipts[i].Status = domain.VerifyInconclusive
				pendingReceipts[i].Notes = appendNote(pendingReceipts[i].Notes, bindReason)
			}
		}
		for i, status := range claimStatuses {
			if status == domain.VerifyPassed || status == domain.VerifyFailed {
				claimStatuses[i] = domain.VerifyInconclusive
			}
		}
	} else {
		for i := range pendingReceipts {
			pendingReceipts[i].Binding = binding
		}
	}
	if len(pendingReceipts) == 0 {
		// A batch marker prevents a verifier-less rerun from falling back to an
		// older passing batch.
		pendingReceipts = append(pendingReceipts, k.makeReceipt(c.ID, batchID, revision, receiptSpec{
			Claim: "verification batch had no runnable verifier", Purpose: domain.VerificationPurposeVerifierRun,
			Surface: domain.SurfaceCode, Status: domain.VerifyNotRun, Notes: "no verifier receipt was produced",
		}))
		pendingReceipts[0].Binding = binding
	}
	markStagedReceiptSensitivity(pendingReceipts, stage.evidence)
	if err := k.store.CommitVerificationBundle(c, stage.evidence, pendingReceipts, stage.raws); err != nil {
		if errors.Is(err, casefs.ErrRevisionConflict) {
			err = fmt.Errorf("case changed while verification was committing; discard this run and retry against the current plan: %w", err)
		}
		return errEnvelope(c.ID, k.red.String(err.Error())), err
	}
	for _, move := range moves {
		k.recordPhase(c.ID, move.from, move.to)
	}
	if binding == domain.VerificationBound {
		for _, annotation := range pendingAnnotations {
			warnings = append(warnings, k.annotateBehavior(ctx, c, annotation.tool, annotation.spec, annotation.status, annotation.artifact)...)
		}
	} else if len(pendingAnnotations) > 0 {
		warnings = append(warnings, "behavior annotations skipped because the verifier batch was not bound to one stable workspace state")
	}
	assessmentReceipts := pendingReceipts
	if durableReceipts, readErr := k.store.Verifications(c.ID); readErr == nil {
		assessmentReceipts = durableReceipts
	}

	claimStatements := make([]string, 0, len(claims))
	for _, claim := range claims {
		claimStatements = append(claimStatements, claim.Statement)
	}
	summary := verifySummary(claimStatements, claimStatuses, scope)
	next := []string{"cortex remember — persist the outcome, evidence, and uncertainty once verification is adequate"}
	if hasUnrun(claimStatuses) {
		next = append([]string{"provide the missing verifier spec (browser_spec / terminal_spec) and re-run cortex verify"}, next...)
	}
	env := k.envelope(c, summary, facts, dedupeStr(warnings), next)
	env.Actions = k.redactStructuredActions(structuredNextForCaseAt(c, k.now().UTC(), assessCaseVerification(c, assessmentReceipts)))
	return env, nil
}

// verificationStage keeps verifier output out of the case until the case
// revision/lease CAS and receipt batch can publish one atomic bundle.
type verificationStage struct {
	taskID   string
	evidence []domain.Evidence
	raws     []casefs.RawRecord
}

type behaviorAnnotation struct {
	tool, spec, artifact string
	status               domain.VerificationStatus
}

func (stage *verificationStage) stampAll(k *Kernel, res adapters.Result, acc *[]domain.Evidence) []string {
	rawRef := ""
	if res.Raw != "" {
		rawID := ids.New("raw")
		raw := capRawForStore(k.red.String(res.Raw), k.cfg.Budget.MaxRawOutputBytesPerTool)
		stage.raws = append(stage.raws, casefs.RawRecord{ID: rawID, Content: raw})
		rawRef = fmt.Sprintf("case://%s/raw/%s", stage.taskID, rawID)
	}
	var ids []string
	for _, f := range res.Facts {
		ev := k.buildEvidenceDerived(stage.taskID, res.Tool, f, rawRef, nil)
		stage.evidence = append(stage.evidence, ev)
		*acc = append(*acc, ev)
		ids = append(ids, ev.ID)
	}
	return ids
}

func (stage *verificationStage) stampFact(k *Kernel, tool string, fact adapters.Fact) domain.Evidence {
	ev := k.buildEvidenceDerived(stage.taskID, tool, fact, "", nil)
	stage.evidence = append(stage.evidence, ev)
	return ev
}

func markStagedReceiptSensitivity(receipts []domain.VerificationRecord, evidence []domain.Evidence) {
	sensitive := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		sensitive[item.ID] = item.Sensitivity == domain.SensitivitySensitive
	}
	for i := range receipts {
		for _, evidenceID := range receipts[i].Evidence {
			if sensitive[evidenceID] {
				receipts[i].Sensitive = true
				break
			}
		}
	}
}

// receiptSpec is the input to writeReceipt (a struct keeps the many optional
// fields readable at the call site).
type receiptSpec struct {
	Claim       string
	ClaimID     string
	Actor       string
	Surface     domain.Surface
	Purpose     domain.VerificationPurpose
	Requirement string
	Contract    string
	Tool        string
	Version     string
	Status      domain.VerificationStatus
	Evidence    []string
	Artifact    string
	Notes       string
}

// writeReceipt persists a verification record naming the exact claim it
// supports, its verifier + version, limitation notes, and a sensitivity label.
func (k *Kernel) writeReceipt(taskID string, rev adapters.Revision, r receiptSpec) error {
	record := k.makeReceipt(taskID, "", rev, r)
	if rev.Commit == "" || rev.DirtyDigest == "" {
		record.Binding = domain.VerificationUnbound
	} else {
		record.Binding = domain.VerificationBound
	}
	return k.store.AppendVerification(taskID, record)
}

func (k *Kernel) makeReceipt(taskID, batchID string, rev adapters.Revision, r receiptSpec) domain.VerificationRecord {
	purpose := r.Purpose
	if purpose == "" {
		purpose = domain.VerificationPurposeVerifierRun
	}
	sensitiveText := k.red.Detected(r.Claim) || k.red.Detected(r.Contract) || k.red.Detected(r.Artifact) || k.red.Detected(r.Notes)
	return domain.VerificationRecord{
		ID:              ids.New("vr"),
		BatchID:         batchID,
		Claim:           k.red.String(r.Claim),
		ClaimID:         r.ClaimID,
		Actor:           r.Actor,
		Surface:         r.Surface,
		Purpose:         purpose,
		Requirement:     r.Requirement,
		Contract:        k.red.String(r.Contract),
		Tool:            k.red.String(r.Tool),
		VerifierVersion: k.red.String(r.Version),
		Status:          r.Status,
		Evidence:        r.Evidence,
		Artifact:        k.red.String(r.Artifact),
		Sensitive:       sensitiveText || k.evidenceSensitive(taskID, r.Evidence),
		Revision:        rev.Commit,
		DirtyDigest:     rev.DirtyDigest,
		Notes:           k.red.String(r.Notes),
		Timestamp:       k.now().UTC(),
	}
}

func cloneChangeLease(lease *domain.ChangeLease) *domain.ChangeLease {
	if lease == nil {
		return nil
	}
	copy := *lease
	if lease.ReleasedAt != nil {
		released := *lease.ReleasedAt
		copy.ReleasedAt = &released
	}
	return &copy
}

func sameLeaseIdentity(a, b *domain.ChangeLease) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Actor == b.Actor && a.AcquiredAt.Equal(b.AcquiredAt)
}

func sameRevision(a, b adapters.Revision) bool {
	return a.Commit != "" && a.DirtyDigest != "" && a.Commit == b.Commit && a.DirtyDigest == b.DirtyDigest
}

func appendNote(existing, note string) string {
	if existing == "" {
		return note
	}
	return existing + "; " + note
}

type verificationPhaseMove struct{ from, to domain.Phase }

// prepareVerificationPhase validates the verifier's starting case/owner and
// computes the phase snapshot. CommitVerificationBundle performs the actual
// CAS together with evidence, raw output, and receipts.
func (k *Kernel) prepareVerificationPhase(taskID, actor string, startRevision uint64, startLease *domain.ChangeLease, startLeaseActive bool) (*domain.CaseFile, []verificationPhaseMove, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return nil, nil, err
	}
	if c.Revision != startRevision {
		return nil, nil, fmt.Errorf("case changed while verification ran; discard this run and retry against the current plan")
	}
	if startLeaseActive && !sameLeaseIdentity(startLease, c.ChangeLease) {
		return nil, nil, fmt.Errorf("change ownership changed while verification ran; discard this run and retry as the current owner")
	}
	if !startLeaseActive {
		if err := validateVerificationLease(c, actor, k.now().UTC()); err != nil {
			return nil, nil, err
		}
	}
	// Every phase move routes through the same transition gate the rest of the
	// kernel uses; the switch only decides WHICH legal moves apply for the
	// current phase/mode, and the moves list is recorded after the bundle
	// commits. The planned→changing→verifying compatibility path (a change task
	// verified by an old client that skipped begin-change) is synthesized as two
	// legal steps rather than a hand-rolled status assignment.
	var moves []verificationPhaseMove
	switch c.Status {
	case domain.PhaseVerifying:
		// The bundle still advances the revision as its commit manifest.
	case domain.PhaseChanging:
		moves = append(moves, verificationPhaseMove{c.Status, domain.PhaseVerifying})
		if err := k.transition(c, domain.PhaseVerifying); err != nil {
			return nil, nil, err
		}
	case domain.PhasePlanned:
		if c.Mode == domain.ModeChange {
			moves = append(moves,
				verificationPhaseMove{domain.PhasePlanned, domain.PhaseChanging},
				verificationPhaseMove{domain.PhaseChanging, domain.PhaseVerifying},
			)
			if err := k.transition(c, domain.PhaseChanging); err != nil {
				return nil, nil, err
			}
			if err := k.transition(c, domain.PhaseVerifying); err != nil {
				return nil, nil, err
			}
		} else {
			moves = append(moves, verificationPhaseMove{domain.PhasePlanned, domain.PhaseVerifying})
			if err := k.transition(c, domain.PhaseVerifying); err != nil {
				return nil, nil, err
			}
		}
	default:
		return nil, nil, fmt.Errorf("cannot commit verification in phase %q", c.Status)
	}
	return c, moves, nil
}

func activeLeaseActor(c *domain.CaseFile, now time.Time) string {
	if c != nil && c.ChangeLease != nil && c.ChangeLease.Active(now) {
		return c.ChangeLease.Actor
	}
	return ""
}

func (k *Kernel) normalizeClaims(legacy []string, typed []domain.VerificationClaim) ([]domain.VerificationClaim, error) {
	out := make([]domain.VerificationClaim, 0, len(legacy)+len(typed))
	seenIDs := make(map[string]bool, len(legacy)+len(typed))
	for _, statement := range legacy {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			return nil, fmt.Errorf("verification claim cannot be empty")
		}
		if len(statement) > 2048 {
			return nil, fmt.Errorf("verification claim exceeds 2048 bytes")
		}
		surface := claimSurface(statement)
		claim := domain.VerificationClaim{
			ID: claimID(surface, statement), Statement: statement, Surface: surface,
			Verifier: domain.SurfaceVerifier(surface), Required: true,
		}
		if seenIDs[claim.ID] {
			return nil, fmt.Errorf("verification claim id is duplicated")
		}
		seenIDs[claim.ID] = true
		out = append(out, claim)
	}
	for _, claim := range typed {
		claim.Statement = strings.TrimSpace(claim.Statement)
		if claim.Statement == "" {
			return nil, fmt.Errorf("typed verification claim cannot be empty")
		}
		if !claim.Surface.Valid() {
			return nil, fmt.Errorf("typed verification claim %q has invalid surface %q", claim.Statement, claim.Surface)
		}
		if len(claim.Statement) > domain.MaxAcceptanceCriterionStatementBytes {
			return nil, fmt.Errorf("typed verification claim exceeds %d bytes", domain.MaxAcceptanceCriterionStatementBytes)
		}
		if len(claim.ID) > 128 || (claim.ID != "" && !validClaimIdentifier(claim.ID)) {
			return nil, fmt.Errorf("typed verification claim id must contain only letters, digits, dash, or underscore")
		}
		if claim.ID == "" {
			claim.ID = claimID(claim.Surface, claim.Statement)
		}
		if claim.Verifier == "" {
			claim.Verifier = domain.SurfaceVerifier(claim.Surface)
		}
		claim.Verifier = strings.TrimSpace(claim.Verifier)
		claim.Contract = strings.TrimSpace(claim.Contract)
		if claim.Contract == "" {
			return nil, fmt.Errorf("typed verification claim needs an exact verifier contract")
		}
		if len(claim.Verifier) > 128 || len(claim.Contract) > 1024 {
			return nil, fmt.Errorf("typed verification claim verifier/contract is too long")
		}
		if k.red.Detected(claim.Contract) {
			return nil, fmt.Errorf("typed verification claim contract must not contain secret-shaped text")
		}
		if err := claim.Validate(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(claim.Verifier, "command:") {
			name := strings.TrimPrefix(claim.Verifier, "command:")
			if _, ok := k.cfg.Verifiers[name]; !ok {
				return nil, fmt.Errorf("typed verification claim %q names unknown configured verifier %q", claim.Statement, name)
			}
		}
		if seenIDs[claim.ID] {
			return nil, fmt.Errorf("verification claim id is duplicated")
		}
		seenIDs[claim.ID] = true
		claim.Required = true
		out = append(out, claim)
	}
	return out, nil
}

func (k *Kernel) validateStableClaimIdentities(taskID string, claims []domain.VerificationClaim) error {
	if len(claims) == 0 {
		return nil
	}
	receipts, err := k.store.Verifications(taskID)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		for _, receipt := range receipts {
			if receipt.EffectivePurpose() != domain.VerificationPurposeNamedClaim || receipt.ClaimID == "" || receipt.ClaimID != claim.ID {
				continue
			}
			if receipt.Claim != k.red.String(claim.Statement) || receipt.Surface != claim.Surface ||
				receipt.Tool != k.red.String(claim.Verifier) || receipt.Contract != k.red.String(claim.Contract) {
				return fmt.Errorf("verification claim id %q already identifies a different statement, surface, verifier, or contract", claim.ID)
			}
		}
	}
	return nil
}

func validClaimIdentifier(id string) bool {
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return id != ""
}

func verificationTarget(verifier, contract string) string {
	return verifier + "\x00" + contract
}

func mergeVerificationStatus(statuses map[string]domain.VerificationStatus, key string, status domain.VerificationStatus) {
	statuses[key] = worseStatus(statuses[key], status)
}

func claimID(surface domain.Surface, statement string) string {
	sum := sha256.Sum256([]byte(string(surface) + "\x00" + statement))
	return fmt.Sprintf("claim_%x", sum[:8])
}

func behavioralRequirement(surface domain.Surface) string {
	if surface == domain.SurfaceBrowser {
		return "cairntrace_flow"
	}
	return "glyphrun_flow"
}

func commandVerificationStatus(res adapters.Result) domain.VerificationStatus {
	switch res.Verdict {
	case adapters.VerdictPassed:
		return domain.VerifyPassed
	case adapters.VerdictFailed:
		return domain.VerifyFailed
	case adapters.VerdictErrored:
		return domain.VerifyInconclusive
	}
	if res.Status == adapters.StatusUnavailable || res.Status == adapters.StatusBlocked || res.Status == adapters.StatusError {
		return domain.VerifyBlocked
	}
	return domain.VerifyInconclusive
}

func commandLimitation(res adapters.Result, status domain.VerificationStatus) string {
	switch status {
	case domain.VerifyPassed:
		return "configured command exited successfully for this exact revision/diff"
	case domain.VerifyFailed:
		return "configured command ran and exited non-zero; inspect the linked raw evidence"
	case domain.VerifyBlocked:
		return "configured command could not run"
	default:
		return firstNonEmptyStr(res.Summary, "configured command result was inconclusive")
	}
}

// currentRevision captures the exact HEAD + dirty tree a verifier is about to
// inspect. A capture failure is visible and leaves revision fields empty rather
// than falsely binding the receipt to the task's start commit.
func (k *Kernel) currentRevision(ctx context.Context) (adapters.Revision, string) {
	if k.git == nil {
		return adapters.Revision{}, "could not bind verification receipts to a revision: git adapter unavailable"
	}
	rev, err := k.git.CurrentRevision(ctx, k.cfg.Workspace)
	if err != nil {
		return adapters.Revision{}, "could not bind verification receipts to current HEAD/diff: " + err.Error()
	}
	return rev, ""
}

// toolVersion returns a verifier tool's version, best-effort.
func (k *Kernel) toolVersion(ctx context.Context, tool string) string {
	if a, ok := k.reg.Get(tool).(interface {
		Version(context.Context) string
	}); ok {
		return a.Version(ctx)
	}
	return ""
}

// evidenceSensitive reports whether any of the linked evidence records is
// sensitive, so the receipt (and its artifact) can be labeled to prevent
// careless archival of secret-adjacent material.
func (k *Kernel) evidenceSensitive(taskID string, ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	all, err := k.store.Evidence(taskID)
	if err != nil {
		return false
	}
	for _, e := range all {
		if want[e.ID] && e.Sensitivity == domain.SensitivitySensitive {
			return true
		}
	}
	return false
}

// reviewStateWord renders a review surface status for a warning, treating the
// zero value (no review ran) as "not run".
func reviewStateWord(st domain.VerificationStatus) string {
	if st == "" {
		return "not run"
	}
	return string(st)
}

// reviewStatus maps a codemap review's adapter status to a verification status:
// an authoritative (indexed) review passes, a partial (unindexed) review is
// inconclusive, and anything else is blocked.
func reviewStatus(s adapters.Status) domain.VerificationStatus {
	switch s {
	case adapters.StatusAuthoritative:
		return domain.VerifyPassed
	case adapters.StatusPartial:
		return domain.VerifyInconclusive
	default:
		return domain.VerifyBlocked
	}
}

// capabilityStatus maps a value-free artifact/capability probe into a receipt.
// Only an authoritative result passes; partial means the tool answered but could
// not establish the capability, while missing/policy/error states are blocked.
func capabilityStatus(res adapters.Result) domain.VerificationStatus {
	switch res.Status {
	case adapters.StatusAuthoritative:
		return domain.VerifyPassed
	case adapters.StatusPartial:
		return domain.VerifyInconclusive
	default:
		return domain.VerifyBlocked
	}
}

func capabilityLimitation(surface domain.Surface, st domain.VerificationStatus) string {
	switch st {
	case domain.VerifyPassed:
		if surface == domain.SurfaceSecret {
			return "tvault project availability is proven value-free; secret-dependent runtime behavior still needs scoped execution"
		}
		return ""
	case domain.VerifyInconclusive:
		return "the verifier answered but could not establish the requested artifact/capability"
	default:
		return "the verifier was unavailable, refused, or errored; the requested artifact/capability is not proven"
	}
}

// behavioralStatus classifies a behavioral run into a verification status,
// distinguishing a genuine failure from an ambiguous errored run — the latter
// is inconclusive, never a failed verdict.
func behavioralStatus(res adapters.Result) domain.VerificationStatus {
	if res.Status == adapters.StatusUnavailable {
		return domain.VerifyBlocked
	}
	// Prefer the adapter's structured verdict — the pass/fail/errored outcome is
	// carried explicitly so the classification does not ride on warning text that
	// could drift (review 2026-07-07). The warning scan below remains only as a
	// fallback for a result that carries no verdict.
	switch res.Verdict {
	case adapters.VerdictPassed:
		return domain.VerifyPassed
	case adapters.VerdictFailed:
		return domain.VerifyFailed
	case adapters.VerdictErrored:
		return domain.VerifyInconclusive
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "run ERRORED (ambiguous") {
			return domain.VerifyInconclusive
		}
		if strings.Contains(w, "verification did NOT pass") {
			return domain.VerifyFailed
		}
	}
	if res.Status == adapters.StatusAuthoritative {
		return domain.VerifyPassed
	}
	return domain.VerifyInconclusive
}

func firstArtifactURI(res adapters.Result) string {
	if len(res.Artifacts) > 0 {
		return res.Artifacts[0].URI
	}
	return ""
}

// stashRunBundle durably archives a behavioral run bundle when it is worth
// keeping: failed browser/terminal runs have high debugging
// value) but not passing ones (low future value). On success it returns an
// fcheap:// reference for the receipt so the evidence is linked back to the
// case file. On a pass, missing bundle, or unavailable fcheap it
// returns the ephemeral run-bundle URI unchanged.
func (k *Kernel) stashRunBundle(ctx context.Context, c *domain.CaseFile, res adapters.Result, passed bool, surface string) (string, []string) {
	uri := firstArtifactURI(res)
	if passed || uri == "" {
		return uri, nil // passing runs are not worth archiving
	}
	fc, ok := k.reg.Get("fcheap").(*adapters.Fcheap)
	if !ok {
		return uri, nil
	}
	tags := memoryTags(c, surface, "failed-run")
	stashID, err := fc.Save(ctx, k.cfg.Workspace, uri, tags, res.Tool)
	k.recordWriteAs(commandActorFromContext(ctx), c.ID, "fcheap", "save", err)
	if err != nil || stashID == "" {
		msg := "no fcheap"
		if err != nil {
			msg = err.Error()
		}
		return uri, []string{fmt.Sprintf("failed %s run not archived to fcheap (%s) — evidence kept at %s", surface, clipStr(msg, 60), uri)}
	}
	return "fcheap://stash/" + stashID, []string{fmt.Sprintf("archived failed %s run to fcheap://stash/%s", surface, stashID)}
}

// annotateBehavior links a proven or meaningfully-failed behavior to the code
// symbols the task DECLARED it would change — the declared change boundary is
// the reasonable-confidence identification of the owning symbol, so Cortex does
// not guess when none is declared. It annotates
// only definitive pass/fail outcomes (an inconclusive/errored run teaches
// nothing), tags the annotation with the verifier as its source, and is
// best-effort: a failure is a warning, never a hard error.
func (k *Kernel) annotateBehavior(ctx context.Context, c *domain.CaseFile, source, spec string, st domain.VerificationStatus, artifact string) []string {
	if st != domain.VerifyPassed && st != domain.VerifyFailed {
		return nil
	}
	cm, ok := k.reg.Get("codemap").(*adapters.Codemap)
	if !ok || len(c.ChangeBoundary.Symbols) == 0 {
		return nil
	}
	note := fmt.Sprintf("cortex: %s behavior %q %s (task %s); evidence %s", source, spec, st, c.ID, firstNonEmptyStr(artifact, "—"))
	var warns []string
	for _, sym := range c.ChangeBoundary.Symbols {
		err := cm.Annotate(ctx, k.cfg.Workspace, sym, source, note)
		k.recordWriteAs(commandActorFromContext(ctx), c.ID, "codemap", "annotate", err)
		if err != nil {
			warns = append(warns, fmt.Sprintf("codemap annotation of %s skipped: %s", sym, clipStr(err.Error(), 60)))
		}
	}
	return warns
}

// claimSurface infers the verification surface a claim belongs to.
// Terminal is checked before browser, and the browser hint is the spaced " ui "
// token, so a "tui"/"cli" claim (or any word merely containing "ui" like "build"
// or "requires") is not misrouted to the browser surface — mirroring
// domain.RouteFor.
func claimSurface(claim string) domain.Surface {
	// Pad so spaced tokens (" cli "/" tui "/" ui ") match whole words only —
	// "click" must not match " cli ", and "build"/"requires" must not match " ui ".
	q := " " + strings.ToLower(claim) + " "
	switch {
	case containsWord(q, " cli ", " tui ", "terminal", "prompt", "command", "stdout"):
		return domain.SurfaceTerminal
	case containsWord(q, "page", "redirect", "browser", "click", "render", " ui ", "screen", "button", "login"):
		return domain.SurfaceBrowser
	case containsWord(q, "secret", "credential", "token", "api key", "env var"):
		return domain.SurfaceSecret
	case containsWord(q, "artifact", "output file", "bundle", "stash"):
		return domain.SurfaceArtifact
	default:
		return domain.SurfaceCode
	}
}

func containsWord(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// containsMarker reports whether any warning carries the given stable marker
// substring (e.g. codemap's "diff risk: high" prefix).
func containsMarker(warnings []string, marker string) bool {
	for _, w := range warnings {
		if strings.Contains(w, marker) {
			return true
		}
	}
	return false
}

func hasUnrun(statuses []domain.VerificationStatus) bool {
	for _, s := range statuses {
		if s == domain.VerifyNotRun {
			return true
		}
	}
	return false
}

func verifySummary(claims []string, statuses []domain.VerificationStatus, scope ScopeReport) string {
	if len(claims) == 0 {
		return fmt.Sprintf("verification run complete; scope %s", scope.Scope)
	}
	passed := 0
	for _, s := range statuses {
		if s == domain.VerifyPassed {
			passed++
		}
	}
	return fmt.Sprintf("%d/%d claims verified; scope %s", passed, len(claims), scope.Scope)
}

// claimLimitation notes why a claim's receipt is not a clean pass.
func claimLimitation(st domain.VerificationStatus) string {
	switch st {
	case domain.VerifyNotRun:
		return "no verifier was run for this surface; supply a browser/terminal spec to prove it"
	case domain.VerifyInconclusive:
		return "the verifier ran but could not confirm the claim"
	case domain.VerifyFailed:
		return "the verifier ran and the claim did not hold"
	default:
		return ""
	}
}

// behavioralLimitation notes a limitation on a behavioral receipt.
func behavioralLimitation(res adapters.Result, st domain.VerificationStatus) string {
	if res.Status == adapters.StatusUnavailable {
		return "verifier tool unavailable — behavior not proven"
	}
	switch st {
	case domain.VerifyFailed:
		return "the behavioral run did not pass; see the archived run bundle"
	case domain.VerifyInconclusive:
		return "the run errored (infrastructure/spec problem) — not a behavioral verdict; re-run to get a clean pass/fail"
	default:
		return ""
	}
}

// selectSpecs asks the surface's verifier which specs cover the current change
// (read-only; no browser/terminal launch). Empty on any error or when no tool /
// coverage is available — cortex then simply leaves the claim unverified.
func (k *Kernel) selectSpecs(ctx context.Context, c *domain.CaseFile, surface domain.Surface) []string {
	// Prefer the review base when set (a review diffs base…HEAD); else the task's
	// start commit (a change diffs from where work began).
	sinceRef := firstNonEmptyStr(c.Workspace.BaseRef, c.Workspace.CommitBefore)
	switch surface {
	case domain.SurfaceBrowser:
		if a, ok := k.reg.Get("cairntrace").(*adapters.Cairntrace); ok {
			if paths, err := a.SelectSpecs(ctx, k.cfg.Workspace, sinceRef); err == nil {
				return paths
			}
		}
	case domain.SurfaceTerminal:
		if a, ok := k.reg.Get("glyphrun").(*adapters.Glyphrun); ok {
			if paths, err := a.AffectedSpecs(ctx, k.cfg.Workspace, sinceRef); err == nil {
				return paths
			}
		}
	}
	return nil
}

// surfaceInScope reports whether a surface is in the task's declared set.
func surfaceInScope(c *domain.CaseFile, surface domain.Surface) bool {
	for _, s := range c.Surfaces {
		if s == surface {
			return true
		}
	}
	return false
}

// worseStatus returns the more severe of two verification statuses, so running
// several covering specs reports a failure rather than masking it with a pass.
// Severity (least→most): passed < blocked < inconclusive < not_run < failed.
func worseStatus(a, b domain.VerificationStatus) domain.VerificationStatus {
	if a == "" {
		return b
	}
	rank := func(s domain.VerificationStatus) int {
		switch s {
		case domain.VerifyPassed:
			return 0
		case domain.VerifyBlocked:
			return 1
		case domain.VerifyInconclusive:
			return 2
		case domain.VerifyFailed:
			return 4
		default:
			return 3
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
