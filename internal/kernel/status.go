package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// StatusReport is the detailed view of a task's health (SPEC §10.2 cortex_status).
type StatusReport struct {
	domain.Envelope
	Revision             uint64                  `json:"revision"`
	Actor                string                  `json:"actor,omitempty"`
	ParentTaskID         string                  `json:"parentTaskId,omitempty"`
	ChildTaskIDs         []string                `json:"childTaskIds,omitempty"`
	PausedFrom           domain.Phase            `json:"pausedFrom,omitempty"`
	ChangeLease          *domain.ChangeLease     `json:"changeLease,omitempty"`
	PendingDecision      *domain.Decision        `json:"pendingDecision,omitempty"`
	Mode                 domain.Mode             `json:"mode"`
	Risk                 string                  `json:"risk"`
	Workspace            domain.Workspace        `json:"workspace"`
	Surfaces             []domain.Surface        `json:"surfaces"`
	UnresolvedHypotheses []domain.HypView        `json:"unresolvedHypotheses,omitempty"`
	VerificationRequired []string                `json:"verificationRequired,omitempty"`
	VerificationDone     []string                `json:"verificationDone,omitempty"`
	VerificationOutcome  VerificationOutcome     `json:"verificationOutcome"`
	StaleVerification    []string                `json:"staleVerification,omitempty"`
	MissingVerification  []string                `json:"missingVerification,omitempty"`
	Scope                *ScopeReport            `json:"scope,omitempty"`
	ToolHealth           []adapters.HealthReport `json:"toolHealth,omitempty"`
	EvidenceCount        int                     `json:"evidenceCount"`
	InvestigationRounds  int                     `json:"investigationRounds"`
	InvestigationBudget  int                     `json:"investigationBudget"`
}

// Status returns the task phase, unresolved hypotheses, scope drift, required
// verification, and tool health (SPEC §10.2 cortex_status).
func (k *Kernel) Status(ctx context.Context, taskID, detail string) (StatusReport, error) {
	detail = strings.ToLower(strings.TrimSpace(detail))
	if detail == "" {
		detail = "standard"
	}
	if detail != "standard" && detail != "full" {
		return StatusReport{Envelope: errEnvelope(taskID, "status detail must be standard or full")}, nil
	}
	snapshot, err := k.store.StatusSnapshot(taskID)
	if err != nil {
		return StatusReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	c := snapshot.Case
	hyps := snapshot.Hypotheses
	receipts := snapshot.Verifications
	decisions := snapshot.Decisions
	currentRev, revErr := adapters.Revision{}, error(nil)
	if !c.Status.IsTerminal() {
		if k.git != nil {
			currentRev, revErr = k.git.CurrentRevision(ctx, k.cfg.Workspace)
		} else {
			revErr = fmt.Errorf("git adapter unavailable")
		}
	}

	rep := StatusReport{
		Envelope: domain.Envelope{
			OK: true, TaskID: c.ID, Phase: c.Status,
			Summary: fmt.Sprintf("task %s is %s (%s)", c.ID, c.Status, clipStr(c.Goal, 60)),
		},
		Mode: c.Mode, Risk: c.Risk, Workspace: c.Workspace, Surfaces: c.Surfaces,
		Revision: c.Revision, Actor: c.Actor, ParentTaskID: c.ParentTaskID,
		ChildTaskIDs: append([]string(nil), c.ChildTaskIDs...), PausedFrom: c.PausedFrom,
		ChangeLease:          c.ChangeLease,
		VerificationRequired: c.VerificationRequired,
		EvidenceCount:        snapshot.EvidenceTotal,
		InvestigationRounds:  c.InvestigationRounds,
		InvestigationBudget:  k.cfg.Budget.MaxInvestigationRounds,
	}
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			decision := decisions[i]
			rep.PendingDecision = &decision
			break
		}
	}
	for _, h := range hyps {
		if h.Status == domain.HypActive || h.Status == domain.HypChallenged {
			rep.UnresolvedHypotheses = append(rep.UnresolvedHypotheses, domain.ToHypView(h))
		}
	}

	// Required vs done verification (SPEC acceptance: status detects missing
	// verification). Only fresh receipts participate in the canonical assessment.
	freshReceipts, staleReceipts := verificationReceiptsAtRevision(receipts, currentRev, revErr)
	for _, r := range staleReceipts {
		rep.StaleVerification = append(rep.StaleVerification, verificationReceiptDisplayID(r))
	}
	for _, r := range freshReceipts {
		if r.Proven() {
			rep.VerificationDone = append(rep.VerificationDone, r.Claim)
		}
	}
	assessment := assessVerification(c.VerificationRequired, freshReceipts)
	rep.VerificationOutcome = assessment.Outcome
	rep.MissingVerification = assessment.MissingRequired
	rep.Actions = hydrateDecisionActions(c, structuredNextForCaseAt(c, k.now().UTC(), assessment), decisions)

	// Scope drift for in-flight change tasks.
	if c.Mode == domain.ModeChange && (c.Status == domain.PhaseChanging || c.Status == domain.PhaseVerifying) && k.git != nil {
		changed, _ := k.git.ChangedFiles(ctx, k.cfg.Workspace, c.Workspace.BaseRef, false)
		sr := k.detectScopeDrift(ctx, c, changed)
		rep.Scope = &sr
		if sr.Scope == "drift_detected" {
			rep.Warnings = append(rep.Warnings, "scope drift detected — see scope.unexpectedFiles")
		}
	}

	if detail == "full" {
		rep.ToolHealth = k.reg.Health(ctx)
	}

	if len(rep.MissingVerification) > 0 && c.Status != domain.PhaseComplete {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d required verification(s) still missing", len(rep.MissingVerification)))
	}
	if len(assessment.FailedClaims) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d current named claim(s) failed", len(assessment.FailedClaims)))
	} else if len(assessment.NonPassingClaims) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d current named claim(s) did not pass", len(assessment.NonPassingClaims)))
	}
	if revErr != nil {
		rep.Warnings = append(rep.Warnings, "could not check verification freshness: "+revErr.Error())
	}
	if len(rep.StaleVerification) > 0 && assessment.Outcome != VerificationVerified {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d verification receipt(s) are stale because HEAD or the dirty diff changed", len(rep.StaleVerification)))
		rep.NextActions = append([]string{"cortex verify — rerun verifiers for the current revision/diff"}, nextForPhase(c.Status)...)
		k.redactStatusReport(&rep)
		return rep, nil
	}
	rep.NextActions = nextForPhase(c.Status)
	if c.Status == domain.PhaseVerifying && assessment.Outcome == VerificationVerified {
		rep.NextActions = []string{"cortex remember — preserve the verified outcome"}
	}
	k.redactStatusReport(&rep)
	return rep, nil
}

func (k *Kernel) redactStatusReport(rep *StatusReport) {
	rep.Summary = k.red.String(rep.Summary)
	rep.Actor = k.red.String(rep.Actor)
	rep.Workspace.Root = k.red.String(rep.Workspace.Root)
	rep.Workspace.Repository = k.red.String(rep.Workspace.Repository)
	rep.Workspace.Branch = k.red.String(rep.Workspace.Branch)
	rep.Workspace.BaseRef = k.red.String(rep.Workspace.BaseRef)
	for i := range rep.UnresolvedHypotheses {
		rep.UnresolvedHypotheses[i].Statement = k.red.String(rep.UnresolvedHypotheses[i].Statement)
	}
	rep.VerificationDone = k.redactStrings(rep.VerificationDone)
	rep.StaleVerification = k.redactStrings(rep.StaleVerification)
	rep.MissingVerification = k.redactStrings(rep.MissingVerification)
	redactDecision(k.red, rep.PendingDecision)
	rep.Warnings = k.redactStrings(rep.Warnings)
	rep.NextActions = k.redactStrings(rep.NextActions)
	rep.Actions = k.redactStructuredActions(rep.Actions)
	for i := range rep.ToolHealth {
		rep.ToolHealth[i].Detail = k.red.String(rep.ToolHealth[i].Detail)
	}
}

// receiptStale is backward-compatible: legacy receipts without a dirty digest
// retain their historical semantics. New receipts are stale when either HEAD
// or the exact dirty tree differs from the state verified.
func receiptStale(r domain.VerificationRecord, current adapters.Revision) bool {
	// An explicitly unbound receipt is the authoritative result of its latest
	// verifier batch. It must stay current enough to mask older proof; treating
	// it as stale could resurrect an earlier passing batch after a mid-run edit.
	if r.Binding == domain.VerificationUnbound {
		return false
	}
	if r.DirtyDigest == "" || current.Commit == "" {
		return false
	}
	return r.Revision != current.Commit || r.DirtyDigest != current.DirtyDigest
}

// verificationReceiptsAtRevision is the single freshness projection used by
// status, completion, metrics, show/Studio, and handoff. When freshness cannot
// be checked, definitive receipts remain in their latest batch but become
// explicitly unbound so an older pass cannot be reported as current.
func verificationReceiptsAtRevision(receipts []domain.VerificationRecord, current adapters.Revision, revisionErr error) (fresh, stale []domain.VerificationRecord) {
	fresh = make([]domain.VerificationRecord, 0, len(receipts))
	for _, receipt := range receipts {
		if revisionErr != nil && receipt.Definitive() {
			receipt.Binding = domain.VerificationUnbound
		}
		if receiptStale(receipt, current) {
			stale = append(stale, receipt)
			continue
		}
		fresh = append(fresh, receipt)
	}
	return fresh, stale
}

func requiredSurface(required string) domain.Surface {
	switch {
	case containsWord(required, "cairn", "browser"):
		return domain.SurfaceBrowser
	case containsWord(required, "glyph", "terminal"):
		return domain.SurfaceTerminal
	case containsWord(required, "fcheap", "artifact"):
		return domain.SurfaceArtifact
	case containsWord(required, "tvault", "secret"):
		return domain.SurfaceSecret
	default:
		return domain.SurfaceCode
	}
}

func nextForPhase(p domain.Phase) []string {
	switch p {
	case domain.PhaseInvestigating:
		return []string{"cortex investigate", "cortex plan"}
	case domain.PhasePlanned:
		return []string{"cortex begin-change — claim bounded change ownership", "make edits within the boundary", "cortex verify"}
	case domain.PhaseChanging:
		return []string{"cortex verify"}
	case domain.PhaseVerifying:
		return []string{"cortex verify (rerun with specs)", "cortex remember"}
	case domain.PhasePersisting:
		return []string{"cortex remember"}
	case domain.PhaseComplete:
		return []string{"task complete"}
	default:
		return nil
	}
}

// AbortTask stops the active task without deleting evidence (SPEC §10.2
// cortex_abort_task). A reason is required.
func (k *Kernel) AbortTask(taskID, reason string) (domain.Envelope, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errEnvelope(taskID, "abort requires a reason"), nil
	}
	reason = k.red.String(reason)
	c, err := k.store.Load(taskID)
	if err != nil {
		return errEnvelope(taskID, err.Error()), nil
	}
	if c.Status.IsTerminal() {
		return errEnvelope(taskID, fmt.Sprintf("task is already in terminal phase %q", c.Status)), nil
	}
	from := c.Status
	if c.ChangeLease != nil && c.ChangeLease.Active(k.now().UTC()) {
		if err := c.ChangeLease.Release(c.ChangeLease.Actor, k.now().UTC()); err != nil {
			return errEnvelope(c.ID, "cannot release change lease: "+err.Error()), nil
		}
	}
	c.Status = domain.PhaseAbandoned
	c.BlockedReason = reason
	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	k.recordPhase(c.ID, from, domain.PhaseAbandoned) // abort bypasses transition()
	return domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
		Summary: "task aborted: " + reason, RawAvailable: false}, nil
}

// ReadEvidence returns a full evidence record (SPEC §10.4 raw retrieval).
func (k *Kernel) ReadEvidence(taskID, evidenceID string) (domain.Evidence, error) {
	evidence, err := k.store.GetEvidence(taskID, evidenceID)
	if err != nil {
		return domain.Evidence{}, err
	}
	sensitive := evidence.Sensitivity == domain.SensitivitySensitive || k.red.Detected(evidence.Claim) ||
		k.red.Detected(evidence.Source.URI) || k.red.Detected(evidence.Source.Actor)
	evidence.Claim = k.red.String(evidence.Claim)
	evidence.Source.URI = k.red.String(evidence.Source.URI)
	evidence.Source.Actor = k.red.String(evidence.Source.Actor)
	if evidence.Location != nil {
		sensitive = sensitive || k.red.Detected(evidence.Location.File) || k.red.Detected(evidence.Location.Symbol)
		evidence.Location.File = k.red.String(evidence.Location.File)
		evidence.Location.Symbol = k.red.String(evidence.Location.Symbol)
	}
	if sensitive {
		evidence.Sensitivity = domain.SensitivitySensitive
	}
	return evidence, nil
}

// ListTasks returns a compact index of all tasks in the store, newest first.
func (k *Kernel) ListTasks() ([]TaskSummary, error) {
	idsList, err := k.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]TaskSummary, 0, len(idsList))
	for _, id := range idsList {
		c, err := k.store.Load(id)
		if err != nil {
			continue
		}
		// Central storage groups by repo *basename* (Slug), so two repos sharing a
		// basename share one store dir. `list` is a per-workspace view, so keep only
		// this workspace's cases (cross-repo `cortex sessions` shows all of them).
		if c.Workspace.Root != k.cfg.Workspace {
			continue
		}
		out = append(out, TaskSummary{
			ID: c.ID, Goal: k.red.String(c.Goal), Phase: c.Status,
			Repository: k.red.String(c.Workspace.Repository), CreatedAt: c.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return out, nil
}

// TaskSummary is a one-line task index entry.
type TaskSummary struct {
	ID         string       `json:"id"`
	Goal       string       `json:"goal"`
	Phase      domain.Phase `json:"phase"`
	Repository string       `json:"repository"`
	CreatedAt  string       `json:"createdAt"`
}
