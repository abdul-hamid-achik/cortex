package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
)

// VerifyInput parameterizes Verify (SPEC §10.2 cortex_verify).
type VerifyInput struct {
	TaskID       string
	Claims       []string
	ChangedFiles []string // optional; derived from git when empty
	BrowserSpec  string   // cairn spec path (proves browser claims)
	TerminalSpec string   // glyph spec path (proves terminal claims)
	// DisableAutoSpecs turns OFF verify-time auto-selection of covering specs
	// (default on). Auto-selection runs only when no explicit spec is given for a
	// declared behavioral surface and a diff exists.
	DisableAutoSpecs bool
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
// supported (SPEC §14). It runs a structural diff review, executes any provided
// behavioral specs, checks for scope drift, and writes a receipt per claim. A
// claim with no relevant verifier is recorded not_run — never rendered as
// passed (SPEC §14.2).
func (k *Kernel) Verify(ctx context.Context, in VerifyInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if err := k.advanceToVerifying(c); err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}

	// Determine changed files (input wins; else diff against the review base if
	// set, otherwise the working tree).
	changed := in.ChangedFiles
	var warnings []string
	if len(changed) == 0 && k.git != nil {
		cf, cfErr := k.git.ChangedFiles(ctx, k.cfg.Workspace, c.Workspace.BaseRef, false)
		if cfErr != nil {
			warnings = append(warnings, "could not detect changed files via git: "+cfErr.Error()+" — scope drift may be incomplete; pass --changed-files to be precise")
		} else {
			changed = cf
		}
	}
	scope := k.detectScopeDrift(ctx, c, changed)

	var facts []domain.Evidence
	// surfaceStatus records each surface's verifier outcome this run. Surfaces
	// with no entry were not verified at all.
	surfaceStatus := map[domain.Surface]domain.VerificationStatus{}

	// 1) Structural review (always, for a change task with a diff). A review is
	// passed only when codemap is indexed; an unindexed review is inconclusive.
	if len(changed) > 0 {
		res := k.run(ctx, "codemap", adapters.Request{TaskID: c.ID, Operation: "review", Input: map[string]any{"since": c.Workspace.BaseRef}})
		warnings = append(warnings, res.Warnings...)
		evs := k.stampAll(c.ID, res, &facts)
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
		if err := k.writeReceipt(c.ID, receiptSpec{Claim: "structural review of the diff", Surface: domain.SurfaceCode,
			Tool: "codemap", Version: k.toolVersion(ctx, "codemap"), Status: st, Evidence: evs, Notes: reviewNote}); err != nil {
			warnings = append(warnings, "could not persist review receipt: "+err.Error())
		}
	}

	// Change-control rigor for change tasks (SPEC §6.2, §13.3):
	if c.Mode == domain.ModeChange {
		if len(changed) == 0 {
			// §6.2: changing → verifying expects a diff/change record to exist.
			warnings = append(warnings, "no diff/change record detected — a change task should have edits before verifying (SPEC §6.2)")
		} else if c.Risk == "medium" || c.Risk == "high" {
			// §13.3: medium/high-risk tasks SHALL have a passing structural review.
			if st := surfaceStatus[domain.SurfaceCode]; st != domain.VerifyPassed {
				warnings = append(warnings, fmt.Sprintf("%s-risk change requires a structural diff review that passed, but codemap review is %s — run `codemap index` and re-verify (SPEC §13.3)",
					c.Risk, reviewStateWord(st)))
			}
		}
	}

	// 2) Behavioral verifiers. An explicit spec wins; otherwise, when the surface
	// is in the task's declared set and a diff exists, cortex auto-selects the
	// specs whose coverage intersects the change (cairn --select-only / glyph
	// affected-specs) and runs them — turning a not_run receipt into a real
	// verification instead of requiring the agent to name the spec (SPEC §14.1).
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
			evs := k.stampAll(c.ID, res, &facts)
			st := behavioralStatus(res)
			artifact, w := k.stashRunBundle(ctx, c, res, st == domain.VerifyPassed, string(bs.surface))
			warnings = append(warnings, w...)
			// The strongest evidence wins per surface: a pass on any covering spec
			// proves it, but a failure on any covering spec must not be masked.
			surfaceStatus[bs.surface] = worseStatus(surfaceStatus[bs.surface], st)
			label := string(bs.surface) + " flow "
			if auto {
				label = "auto-selected " + label
			}
			if err := k.writeReceipt(c.ID, receiptSpec{Claim: label + spec, Surface: bs.surface,
				Tool: bs.tool, Version: k.toolVersion(ctx, bs.tool), Status: st, Evidence: evs,
				Artifact: artifact, Notes: behavioralLimitation(res, st)}); err != nil {
				warnings = append(warnings, "could not persist "+string(bs.surface)+" receipt: "+err.Error())
			}
			warnings = append(warnings, k.annotateBehavior(ctx, c, bs.tool, spec, st, artifact)...)
		}
	}

	// 3) Map each named claim to a verifier receipt (SPEC §14.1). Track the
	// structured status per claim — never derive the pass count from strings
	// that embed the free-text claim (a claim mentioning "passed" must not be
	// counted as verified; SPEC §14.2).
	var claimStatuses []domain.VerificationStatus
	for _, claim := range in.Claims {
		surf := claimSurface(claim)
		st, ran := surfaceStatus[surf]
		if !ran {
			st = domain.VerifyNotRun
			warnings = append(warnings, fmt.Sprintf("claim %q needs a %s verifier that was not run", clipStr(claim, 50), surf))
		}
		if err := k.writeReceipt(c.ID, receiptSpec{Claim: claim, Surface: surf, Tool: domain.SurfaceVerifier(surf),
			Status: st, Notes: claimLimitation(st)}); err != nil {
			warnings = append(warnings, "could not persist claim receipt: "+err.Error())
		}
		claimStatuses = append(claimStatuses, st)
	}

	// Scope drift is a warning, not a failure (SPEC §13.2).
	if scope.Scope == "drift_detected" {
		warnings = append(warnings, fmt.Sprintf("scope drift (%s risk): %s changed outside the boundary — %s",
			scope.Risk, pluralizeGeneric(len(scope.UnexpectedFiles), "file", "files"), scope.Action))
		if ev, err := k.stampEvidence(c.ID, "git", adapters.Fact{Kind: "code_location", Confidence: "high",
			Claim: "scope drift: changed outside declared boundary: " + strings.Join(scope.UnexpectedFiles, ", ")}); err == nil {
			facts = append(facts, ev)
		}
	}

	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}

	summary := verifySummary(in.Claims, claimStatuses, scope)
	next := []string{"cortex remember — persist the outcome, evidence, and uncertainty once verification is adequate"}
	if hasUnrun(claimStatuses) {
		next = append([]string{"provide the missing verifier spec (browser_spec / terminal_spec) and re-run cortex verify"}, next...)
	}
	env := k.envelope(c, summary, facts, dedupeStr(warnings), next)
	return env, nil
}

// advanceToVerifying moves a case through changing into verifying, honoring the
// phase graph. Cortex does not mutate code — the agent harness does — so by the
// time verify is called the diff (if any) already exists (SPEC §24 #2).
func (k *Kernel) advanceToVerifying(c *domain.CaseFile) error {
	switch c.Status {
	case domain.PhaseVerifying:
		return nil
	case domain.PhaseChanging:
		return k.transition(c, domain.PhaseVerifying)
	case domain.PhasePlanned:
		if c.Mode == domain.ModeChange {
			if err := k.transition(c, domain.PhaseChanging); err != nil {
				return err
			}
			return k.transition(c, domain.PhaseVerifying)
		}
		return k.transition(c, domain.PhaseVerifying)
	default:
		return fmt.Errorf("cannot verify in phase %q; plan the task first", c.Status)
	}
}

// stampAll promotes every fact in a result into evidence (sharing one stored
// raw blob per tool call) and appends to acc.
func (k *Kernel) stampAll(taskID string, res adapters.Result, acc *[]domain.Evidence) []string {
	rawRef := k.storeRaw(taskID, res)
	var ids []string
	for _, f := range res.Facts {
		if ev, err := k.stampEvidenceRaw(taskID, res.Tool, f, rawRef); err == nil {
			*acc = append(*acc, ev)
			ids = append(ids, ev.ID)
		}
	}
	return ids
}

// receiptSpec is the input to writeReceipt (a struct keeps the many optional
// fields readable at the call site).
type receiptSpec struct {
	Claim    string
	Surface  domain.Surface
	Tool     string
	Version  string
	Status   domain.VerificationStatus
	Evidence []string
	Artifact string
	Notes    string
}

// writeReceipt persists a verification record naming the exact claim it
// supports, its verifier + version, limitation notes, and a sensitivity label
// (SPEC §14.3, §16.2 #5).
func (k *Kernel) writeReceipt(taskID string, r receiptSpec) error {
	rev := ""
	if c, err := k.store.Load(taskID); err == nil {
		rev = c.Workspace.CommitBefore
	}
	return k.store.AppendVerification(taskID, domain.VerificationRecord{
		ID:              ids.New("vr"),
		Claim:           r.Claim,
		Surface:         r.Surface,
		Tool:            r.Tool,
		VerifierVersion: r.Version,
		Status:          r.Status,
		Evidence:        r.Evidence,
		Artifact:        r.Artifact,
		Sensitive:       k.evidenceSensitive(taskID, r.Evidence),
		Revision:        rev,
		Notes:           r.Notes,
		Timestamp:       k.now().UTC(),
	})
}

// toolVersion returns a verifier tool's version, best-effort (SPEC §14.3).
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
// careless archival of secret-adjacent material (SPEC §16.2 #5).
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

// behavioralStatus classifies a behavioral run into a verification status,
// distinguishing a genuine failure from an ambiguous errored run — the latter
// is inconclusive, never a failed verdict (SPEC §11.4, §14.2).
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
// keeping — SPEC §12.6 stashes failed browser/terminal runs (high debugging
// value) but not passing ones (low future value). On success it returns an
// fcheap:// reference for the receipt so the evidence is linked back to the
// case file (SPEC §25 #6). On a pass, missing bundle, or unavailable fcheap it
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
	k.recordWrite(c.ID, "fcheap", "save", err)
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
// not guess when none is declared (SPEC §12.2 structural memory). It annotates
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
		k.recordWrite(c.ID, "codemap", "annotate", err)
		if err != nil {
			warns = append(warns, fmt.Sprintf("codemap annotation of %s skipped: %s", sym, clipStr(err.Error(), 60)))
		}
	}
	return warns
}

// claimSurface infers the verification surface a claim belongs to (SPEC §14.1).
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
	case containsWord(q, "artifact", "output file", "bundle"):
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

// claimLimitation notes why a claim's receipt is not a clean pass (SPEC §14.3).
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

// behavioralLimitation notes a limitation on a behavioral receipt (SPEC §14.3).
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
