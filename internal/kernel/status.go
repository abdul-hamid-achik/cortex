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
	Mode                 domain.Mode             `json:"mode"`
	Risk                 string                  `json:"risk"`
	Workspace            domain.Workspace        `json:"workspace"`
	Surfaces             []domain.Surface        `json:"surfaces"`
	UnresolvedHypotheses []domain.HypView        `json:"unresolvedHypotheses,omitempty"`
	VerificationRequired []string                `json:"verificationRequired,omitempty"`
	VerificationDone     []string                `json:"verificationDone,omitempty"`
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
	c, err := k.store.Load(taskID)
	if err != nil {
		return StatusReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	evidence, _ := k.store.Evidence(taskID)
	hyps, _ := k.store.Hypotheses(taskID)
	receipts, _ := k.store.Verifications(taskID)
	currentRev, revErr := adapters.Revision{}, error(nil)
	if k.git != nil {
		currentRev, revErr = k.git.CurrentRevision(ctx, k.cfg.Workspace)
	}

	rep := StatusReport{
		Envelope: domain.Envelope{
			OK: true, TaskID: c.ID, Phase: c.Status,
			Summary: fmt.Sprintf("task %s is %s (%s)", c.ID, c.Status, clipStr(c.Goal, 60)),
		},
		Mode: c.Mode, Risk: c.Risk, Workspace: c.Workspace, Surfaces: c.Surfaces,
		VerificationRequired: c.VerificationRequired,
		EvidenceCount:        len(evidence),
		InvestigationRounds:  c.InvestigationRounds,
		InvestigationBudget:  k.cfg.Budget.MaxInvestigationRounds,
	}

	for _, h := range hyps {
		if h.Status == domain.HypActive || h.Status == domain.HypChallenged {
			rep.UnresolvedHypotheses = append(rep.UnresolvedHypotheses, domain.ToHypView(h))
		}
	}

	// Required vs done verification (SPEC acceptance: status detects missing verification).
	done := map[string]bool{}
	for _, r := range receipts {
		if receiptStale(r, currentRev) {
			rep.StaleVerification = append(rep.StaleVerification, r.Claim)
			continue
		}
		if r.Proven() {
			rep.VerificationDone = append(rep.VerificationDone, r.Claim)
		}
		done[string(r.Surface)] = done[string(r.Surface)] || r.Proven()
	}
	for _, req := range c.VerificationRequired {
		if !verifierSatisfiedCurrent(req, receipts, currentRev) {
			rep.MissingVerification = append(rep.MissingVerification, req)
		}
	}

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
	if revErr != nil {
		rep.Warnings = append(rep.Warnings, "could not check verification freshness: "+revErr.Error())
	}
	if len(rep.StaleVerification) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d verification receipt(s) are stale because HEAD or the dirty diff changed", len(rep.StaleVerification)))
		rep.NextActions = append([]string{"cortex verify — rerun verifiers for the current revision/diff"}, nextForPhase(c.Status)...)
		return rep, nil
	}
	rep.NextActions = nextForPhase(c.Status)
	return rep, nil
}

// verifierSatisfied reports whether a required verifier label has a passing
// receipt. The match is loose: a required "cairntrace_flow" is satisfied by a
// passed browser-surface receipt.
func verifierSatisfied(required string, receipts []domain.VerificationRecord) bool {
	return verifierSatisfiedCurrent(required, receipts, adapters.Revision{})
}

func verifierSatisfiedCurrent(required string, receipts []domain.VerificationRecord, current adapters.Revision) bool {
	surf := requiredSurface(required)
	for _, r := range receipts {
		if receiptStale(r, current) || !r.Proven() {
			continue
		}
		if r.Surface == surf || string(r.Surface) == required || r.Tool == required {
			return true
		}
	}
	return false
}

// receiptStale is backward-compatible: legacy receipts without a dirty digest
// retain their historical semantics. New receipts are stale when either HEAD
// or the exact dirty tree differs from the state verified.
func receiptStale(r domain.VerificationRecord, current adapters.Revision) bool {
	if r.DirtyDigest == "" || current.Commit == "" {
		return false
	}
	return r.Revision != current.Commit || r.DirtyDigest != current.DirtyDigest
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
		return []string{"make edits within the boundary", "cortex verify"}
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
	if reason == "" {
		return errEnvelope(taskID, "abort requires a reason"), nil
	}
	c, err := k.store.Load(taskID)
	if err != nil {
		return errEnvelope(taskID, err.Error()), nil
	}
	if c.Status.IsTerminal() {
		return errEnvelope(taskID, fmt.Sprintf("task is already in terminal phase %q", c.Status)), nil
	}
	from := c.Status
	c.Status = domain.PhaseAbandoned
	c.BlockedReason = reason
	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	k.recordPhase(c.ID, from, domain.PhaseAbandoned) // abort bypasses transition()
	return domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
		Summary: "task aborted: " + reason, RawAvailable: true}, nil
}

// ReadEvidence returns a full evidence record (SPEC §10.4 raw retrieval).
func (k *Kernel) ReadEvidence(taskID, evidenceID string) (domain.Evidence, error) {
	return k.store.GetEvidence(taskID, evidenceID)
}

// ReadArtifact resolves an artifact/raw reference to its content (SPEC §10.4
// cortex_read_artifact). It handles case://<taskID>/raw/<id> refs stored by
// Cortex. For fcheap:// references it returns guidance rather than the bytes —
// fetching a stash is fcheap's job (use `fcheap restore <id>`).
func (k *Kernel) ReadArtifact(taskID, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "fcheap://"):
		id := strings.TrimPrefix(ref, "fcheap://stash/")
		return "", fmt.Errorf("stashed artifact — retrieve it with `fcheap restore %s`", id)
	case strings.Contains(ref, "/raw/"):
		rawID := ref[strings.LastIndex(ref, "/raw/")+len("/raw/"):]
		return k.store.ReadRaw(taskID, rawID)
	case strings.Contains(ref, "/evidence/"):
		return "", fmt.Errorf("this evidence has no stored raw output (the reference self-points)")
	default:
		return "", fmt.Errorf("unrecognized artifact reference %q", ref)
	}
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
		out = append(out, TaskSummary{ID: c.ID, Goal: c.Goal, Phase: c.Status, Repository: c.Workspace.Repository, CreatedAt: c.CreatedAt.Format("2006-01-02 15:04")})
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
