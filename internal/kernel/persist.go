package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// RememberInput parameterizes Remember (SPEC §10.2 cortex_remember).
type RememberInput struct {
	TaskID                  string
	Outcome                 string
	Importance              float64
	Tags                    []string
	VerificationNotPossible bool // explicit acknowledgment when no verifier could run
	// AcceptFailed allows completion when the only definitive receipts are
	// failures (no pass). Without it, a failed browser/terminal/code verdict
	// blocks remember — "the verifier ran" is not the same as "the claim held"
	// (review 2026-07-08). Reviews that REQUEST CHANGES set this so the task can
	// complete with an honest failed outcome.
	AcceptFailed bool
}

// Remember persists a concise, provenance-rich conclusion to durable memory and
// completes the task (SPEC §15). It enforces the completion invariant: a task
// cannot complete without a *passing* verification record, an explicit
// verification-not-possible acknowledgment, or an explicit accept-failed
// acknowledgment when only failed verdicts exist (SPEC §6.3 #2, §25 #2/#4).
func (k *Kernel) Remember(ctx context.Context, in RememberInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if c.Status != domain.PhaseVerifying && c.Status != domain.PhasePersisting {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot remember in phase %q; call cortex_verify first, then cortex_remember", c.Status)), nil
	}
	in.Outcome = strings.TrimSpace(in.Outcome)
	if in.Outcome == "" {
		return errEnvelope(in.TaskID, "remember needs an outcome summary"), nil
	}
	if textExceeds(in.Outcome, maxRecordTextBytes) {
		return errEnvelope(in.TaskID, fmt.Sprintf("remember outcome exceeds %d bytes", maxRecordTextBytes)), nil
	}
	if len(in.Tags) > 64 {
		return errEnvelope(in.TaskID, "remember accepts at most 64 tags"), nil
	}
	for _, tag := range in.Tags {
		if textExceeds(strings.TrimSpace(tag), maxStableIdentifierBytes) {
			return errEnvelope(in.TaskID, fmt.Sprintf("remember tags must be at most %d bytes", maxStableIdentifierBytes)), nil
		}
	}

	receipts, _ := k.store.Verifications(c.ID)
	var verificationWarnings []string
	// A receipt tied to an older HEAD/diff proves a prior workspace state, not
	// the state being completed now. Legacy receipts without dirtyDigest retain
	// their old semantics for on-disk compatibility.
	current := adapters.Revision{}
	var revisionErr error
	if k.git != nil {
		current, revisionErr = k.git.CurrentRevision(ctx, k.cfg.Workspace)
	} else {
		revisionErr = fmt.Errorf("git adapter unavailable")
	}
	receipts, _ = verificationReceiptsAtRevision(receipts, current, revisionErr)
	if revisionErr != nil {
		verificationWarnings = append(verificationWarnings, "could not check verification freshness: "+revisionErr.Error())
	}
	// One canonical assessment drives completion, status, metrics, overview, and
	// review. A pass on one surface cannot launder a failed/unrun named claim or a
	// missing required verifier into a verified result.
	assessment := assessVerification(c.VerificationRequired, receipts)
	switch assessment.Outcome {
	case VerificationVerified:
		// No acknowledgement is needed.
	case VerificationFailed:
		if !in.AcceptFailed {
			return errEnvelope(c.ID, "cannot complete: verification failed. fix the change and re-run cortex verify, or set accept_failed=true to record the failed outcome explicitly"), nil
		}
	case VerificationPartial:
		if !in.VerificationNotPossible {
			return errEnvelope(c.ID, "cannot complete: verification is partial (a required verifier or named claim did not pass). run the missing verification, or set verification_not_possible=true to acknowledge the incomplete result explicitly"), nil
		}
	case VerificationUnverified:
		if !in.VerificationNotPossible {
			return errEnvelope(c.ID, "cannot complete: no adequate verification was performed (receipts are absent, blocked, inconclusive, or not_run). run cortex verify with an available verifier, or set verification_not_possible=true to record explicitly that verification could not be performed"), nil
		}
	}
	fullyVerified := assessment.Outcome == VerificationVerified && !in.VerificationNotPossible && !in.AcceptFailed
	// Completion relinquishes bounded change ownership while retaining the lease
	// record for audit. Expired/released leases need no mutation.
	if c.ChangeLease != nil && c.ChangeLease.Active(k.now().UTC()) {
		if err := c.ChangeLease.Release(c.ChangeLease.Actor, k.now().UTC()); err != nil {
			return errEnvelope(c.ID, "cannot release change lease: "+err.Error()), nil
		}
	}

	// verifying → persisting → complete. Advance the in-memory phase before
	// rendering the summary so it reflects the final state, but append phase
	// history only after case.json commits; otherwise a failed summary write or
	// CAS would leave phantom transitions in the audit ledger.
	type phaseMove struct{ from, to domain.Phase }
	var moves []phaseMove
	if c.Status == domain.PhaseVerifying {
		from := c.Status
		if err := k.transition(c, domain.PhasePersisting); err != nil {
			return errEnvelope(c.ID, err.Error()), nil
		}
		moves = append(moves, phaseMove{from: from, to: c.Status})
	}
	from := c.Status
	if err := k.transition(c, domain.PhaseComplete); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	moves = append(moves, phaseMove{from: from, to: c.Status})

	evidence, _ := k.store.Evidence(c.ID)
	hyps, _ := k.store.Hypotheses(c.ID)
	// Redact at this durable write boundary (SPEC §16.3 #4): summary.md is built
	// from model/human-supplied goal, outcome, hypothesis, and claim text — none
	// of which passed the redactor on the way in (evidence claims did, but these
	// fields did not). Without this, a secret in the outcome ("removed sk_live_…")
	// lands in summary.md in cleartext even though the sibling vecgrep memory of
	// the same text is masked at line ~93. (Found in review 2026-07-07.)
	summary := k.red.String(renderSummary(c, in.Outcome, in.VerificationNotPossible, evidence, hyps, receipts))
	// summary.md is idempotent (a plain overwrite), so writing it before Save is
	// safe on a retry.
	if err := k.store.WriteSummary(c.ID, summary); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}

	// Persist the completed case BEFORE the vecgrep memory write. That memory is
	// APPEND-only (each call adds a new record), so writing it before a Save that
	// then fails would let a retry — still in a valid phase because the failed
	// Save never landed — write a SECOND copy. Saving first makes completion
	// idempotent: once the case is durably complete a retry is refused by the
	// phase gate, and if the Save fails we bail before any append. (review 2026-07-07)
	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	for _, move := range moves {
		k.recordPhase(c.ID, move.from, move.to)
	}

	// Cross-case disproof recall (SPEC §15.4): index all resolved hypotheses
	// and definitive receipts now that the case is durably complete. The
	// just-resolved hypothesis was already indexed at resolve time with its
	// reason; this backfills the rest. Best-effort, background-decoupled.
	k.indexCaseForRecall(context.Background(), c, hyps, receipts)

	warnings := append([]string(nil), verificationWarnings...)
	// Durable semantic memory via vecgrep (best-effort; SPEC §15.1 semantic recall).
	if v, ok := k.reg.Get("vecgrep").(*adapters.Vecgrep); ok {
		tags := memoryTags(c, in.Tags...)
		// Redact the memory line at this write boundary too: it is the most durable,
		// cross-project sink (vecgrep's global store), and its content is built from
		// model-supplied goal/outcome text (SPEC §15.2 "do not remember secrets",
		// §16.2). The confidence reflects ACTUAL verification, never a hardcoded high.
		mem := k.red.String(memoryLine(c, in.Outcome, receipts, memoryConfidence(fullyVerified)))
		err := v.Remember(ctx, k.cfg.Workspace, mem, tags, clampImportance(in.Importance))
		k.recordWrite(c.ID, "vecgrep", "remember", err)
		if err != nil {
			warnings = append(warnings, "durable memory not stored: "+err.Error())
		}
	}

	env := domain.Envelope{
		OK:           true,
		TaskID:       c.ID,
		Phase:        c.Status,
		Summary:      fmt.Sprintf("task %s complete: %s", c.ID, clipStr(k.red.String(in.Outcome), 100)),
		Warnings:     k.redactStrings(warnings),
		NextActions:  []string{"summary written to " + summaryPath(k, c.ID)},
		RawAvailable: false,
	}
	switch assessment.Outcome {
	case VerificationUnverified:
		env.Warnings = append(env.Warnings, "completed WITHOUT verification — the outcome is unverified (no verifier ran)")
	case VerificationFailed:
		env.Warnings = append(env.Warnings, "completed with FAILED verification — the outcome records a failed verifier run (accept_failed)")
	case VerificationPartial:
		gaps := append([]string{}, assessment.MissingRequired...)
		gaps = append(gaps, assessment.NonPassingClaims...)
		env.Warnings = append(env.Warnings, fmt.Sprintf("completed with INCOMPLETE verification — required verifier(s) or named claim(s) not passed: %s. the outcome is only partially verified", strings.Join(dedupeStr(gaps), ", ")))
	}
	// A task that completes with hypotheses still 'active' leaves its hypothesis
	// list showing nothing resolved even though the outcome settled the question
	// (dogfooding 2026-07-07). The task is already terminal, so this is a nudge
	// to resolve BEFORE remembering next time, not a hard gate — the outcome
	// text remains the authoritative record either way.
	if n := countActive(hyps); n > 0 {
		env.Warnings = append(env.Warnings, fmt.Sprintf("%s left unresolved at completion — resolve them with cortex_resolve before cortex_remember so the hypothesis ledger reflects the outcome, not just the prose",
			pluralizeGeneric(n, "hypothesis was", "hypotheses were")))
	}
	return env, nil
}

// hasDefinitiveVerification reports whether any receipt carries a definitive
// verdict (passed or failed) — retained for focused helper tests and callers that
// need that narrower question. Task-level truth uses assessVerification.
func hasDefinitiveVerification(receipts []domain.VerificationRecord) bool {
	return hasPassingVerification(receipts) || hasFailedVerification(receipts)
}

// hasPassingVerification reports whether any receipt is an affirmative pass.
func hasPassingVerification(receipts []domain.VerificationRecord) bool {
	for _, r := range receipts {
		if r.Proven() {
			return true
		}
	}
	return false
}

// hasFailedVerification reports whether any receipt is an explicit fail.
func hasFailedVerification(receipts []domain.VerificationRecord) bool {
	for _, r := range receipts {
		if r.Failed() {
			return true
		}
	}
	return false
}

// memoryTags builds the durable-memory tag set for a case. The repo tag is
// always "repo:<name>" (never the bare repository string) so a project named
// "cortex" does not collapse into the product tag and pollute cross-repo recall
// with every tmp.* test workspace's memories.
func memoryTags(c *domain.CaseFile, extra ...string) []string {
	repo := c.Workspace.Repository
	if repo == "" {
		repo = "unknown"
	}
	tags := append([]string{"cortex", "repo:" + repo}, extra...)
	return dedupeStr(tags)
}

// countActive counts hypotheses still in the active (unresolved) state.
func countActive(hyps []domain.Hypothesis) int {
	n := 0
	for _, h := range hyps {
		if h.Status == domain.HypActive {
			n++
		}
	}
	return n
}

// memoryConfidence maps whether a task's required verification fully passed to
// the confidence band recorded in durable memory (SPEC §8.6: high requires a
// primary source plus successful relevant verification — never restatement).
func memoryConfidence(fullyVerified bool) string {
	if fullyVerified {
		return "high"
	}
	return "medium"
}

func summaryPath(k *Kernel, taskID string) string {
	return strings.TrimRight(k.store.Root(), "/") + "/" + taskID + "/summary.md"
}

// memoryLine renders a durable memory in the SPEC §15.3 format
// (repo/area/symbol/behavior/finding/evidence/confidence/commit) so recalls are
// grounded and reusable, not just a free-text blob.
func memoryLine(c *domain.CaseFile, outcome string, receipts []domain.VerificationRecord, confidence string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "repo=%s area=%s", c.Workspace.Repository, strings.Join(surfaceNames(c.Surfaces), "+"))
	if len(c.ChangeBoundary.Symbols) > 0 {
		fmt.Fprintf(&b, " symbol=%s", strings.Join(c.ChangeBoundary.Symbols, ","))
	}
	fmt.Fprintf(&b, " behavior=%s finding=%s confidence=%s", clipStr(c.Goal, 60), outcome, confidence)
	// evidence: the case id plus any durable artifact refs the receipts linked.
	evidence := "case " + c.ID
	for _, r := range receipts {
		if strings.HasPrefix(r.Artifact, "fcheap://") {
			evidence += "; " + r.Artifact
		}
	}
	fmt.Fprintf(&b, " evidence=%s", evidence)
	if c.Workspace.CommitBefore != "" {
		fmt.Fprintf(&b, " commit=%s", c.Workspace.CommitBefore)
	}
	return b.String()
}

func surfaceNames(ss []domain.Surface) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, string(s))
	}
	return out
}

func clampImportance(v float64) float64 {
	if v <= 0 {
		return 0.5
	}
	if v > 1 {
		return 1
	}
	return v
}

// renderSummary produces the human-readable summary.md (SPEC §8.1).
func renderSummary(c *domain.CaseFile, outcome string, unverified bool, evidence []domain.Evidence, hyps []domain.Hypothesis, receipts []domain.VerificationRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", c.Goal)
	fmt.Fprintf(&b, "- **Task:** `%s`\n- **Repository:** %s @ %s (%s)\n- **Mode:** %s · **Risk:** %s · **Status:** %s\n\n",
		c.ID, c.Workspace.Repository, c.Workspace.CommitBefore, c.Workspace.Branch, c.Mode, c.Risk, c.Status)

	fmt.Fprintf(&b, "## Outcome\n\n%s\n\n", outcome)

	if len(hyps) > 0 {
		b.WriteString("## Hypotheses\n\n")
		for _, h := range hyps {
			fmt.Fprintf(&b, "- **%s** (%s, %s) — disprove by: %s\n", h.Statement, h.Confidence, h.Status, firstNonEmptyStr(h.DisproveBy.Note, h.DisproveBy.Contract, "—"))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Verification\n\n")
	if len(receipts) == 0 {
		b.WriteString("_No verification was performed")
		if unverified {
			b.WriteString(" (explicitly acknowledged as not possible)")
		}
		b.WriteString("._\n\n")
	} else {
		for _, r := range receipts {
			fmt.Fprintf(&b, "- [%s] **%s** — %s (%s)\n", r.Status, r.Claim, r.Tool, r.Surface)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Evidence (%d records)\n\n", len(evidence))
	for _, e := range evidence {
		loc := ""
		if e.Location != nil && e.Location.File != "" {
			loc = " — " + e.Location.File
		}
		fmt.Fprintf(&b, "- `%s` [%s, %s] %s%s\n", e.ID, e.Kind, e.Confidence, clipStr(e.Claim, 120), loc)
	}
	b.WriteString("\n_Generated by Cortex._\n")
	return b.String()
}
