package kernel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

const (
	maxResumeEvidence = 50
	maxResumeFindings = 10
)

// checkpoint rewrites the case's checkpoint.md from the canonical handoff
// projection plus the long-running-task state (coverage, findings, jobs). It
// is best effort and idempotent: called after phase moves, investigation
// rounds, findings, dossier writes, and job completion, so a resumed model
// always has a packet no older than its last durable step.
func (k *Kernel) checkpoint(taskID string) {
	h, err := BuildHandoffIn(k.cfg.Workspace, taskID, k.now())
	if err != nil {
		return
	}
	md := RenderHandoffCompact(h) + k.renderLongRunningState(taskID)
	if err := k.store.WriteCheckpoint(taskID, md); err != nil {
		k.recordWrite(taskID, "cortex", "checkpoint", err)
	}
}

// renderLongRunningState appends survey coverage, the open backlog, and
// detached jobs to a compact packet.
func (k *Kernel) renderLongRunningState(taskID string) string {
	var b strings.Builder
	if ledger := k.coverageFor(taskID); ledger != nil {
		s := ledger.Summary()
		fmt.Fprintf(&b, "\n## Survey coverage\n\n- %.0f%% — %d/%d modules explored, %d summarized, %d unseen", s.Percent, s.Explored+s.Summarized, s.Total, s.Summarized, s.Unseen)
		if s.Next != "" {
			fmt.Fprintf(&b, "\n- next module: `%s`", s.Next)
		}
		b.WriteString("\n")
	}
	if findings, err := k.store.Findings(taskID); err == nil && len(findings) > 0 {
		counts := countFindings(findings)
		fmt.Fprintf(&b, "\n## Findings\n\n- %d total: %d open, %d triaged, %d converted, %d dismissed\n", counts.Total, counts.Open, counts.Triaged, counts.Converted, counts.Dismissed)
		shown := 0
		for _, f := range findings {
			if f.Status.Terminal() || f.Sensitive {
				continue
			}
			if shown >= 5 {
				b.WriteString("- …\n")
				break
			}
			fmt.Fprintf(&b, "- `%s` [%s/%s] %s\n", f.ID, f.Kind, f.Severity, singleLine(clipStr(f.Title, 100)))
			shown++
		}
	}
	if jobs, err := k.store.Jobs(taskID); err == nil {
		var open []string
		for _, j := range jobs {
			if !j.Status.Terminal() {
				open = append(open, fmt.Sprintf("`%s` %s (%d/%d rounds)", j.ID, j.Status, j.RoundsDone, j.RoundsTotal))
			}
		}
		if len(open) > 0 {
			fmt.Fprintf(&b, "\n## Background jobs\n\n- %s\n", strings.Join(open, "\n- "))
		}
	}
	return b.String()
}

// ResumeInput asks for the checkpoint plus everything that changed since a
// point in time (the cursor a resumed agent or a second actor already saw).
type ResumeInput struct {
	TaskID string
	Since  time.Time // zero = only the checkpoint plus the most recent records
	Limit  int
}

// ResumeReport is what a model reads after context loss: the checkpoint
// packet and the deltas since its cursor.
type ResumeReport struct {
	domain.Envelope
	Revision        uint64                  `json:"revision"`
	Checkpoint      string                  `json:"checkpoint"`
	CheckpointFresh bool                    `json:"checkpointFresh"`
	Since           *time.Time              `json:"since,omitempty"`
	Cursor          time.Time               `json:"cursor"`
	Evidence        []domain.FactView       `json:"evidence,omitempty"`
	EvidenceTotal   int                     `json:"evidenceTotal"`
	PhaseEvents     []casefs.PhaseEvent     `json:"phaseEvents,omitempty"`
	PendingDecision *domain.Decision        `json:"pendingDecision,omitempty"`
	Findings        []domain.Finding        `json:"findings,omitempty"`
	Coverage        *domain.CoverageSummary `json:"coverage,omitempty"`
	Jobs            []domain.Job            `json:"jobs,omitempty"`
	StaleEvidence   []StaleEvidence         `json:"staleEvidence,omitempty"`
}

// Resume returns the checkpoint and the deltas since a cursor. It never
// mutates the case; a missing checkpoint is rebuilt on the spot.
func (k *Kernel) Resume(ctx context.Context, in ResumeInput) (ResumeReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return ResumeReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	checkpoint, err := k.store.ReadCheckpoint(c.ID)
	if err != nil {
		return ResumeReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	fresh := true
	if checkpoint == "" {
		k.checkpoint(c.ID)
		checkpoint, _ = k.store.ReadCheckpoint(c.ID)
		fresh = false
	}
	limit := in.Limit
	if limit <= 0 || limit > maxResumeEvidence {
		limit = maxResumeEvidence
	}
	evidence, err := k.store.Evidence(c.ID)
	if err != nil {
		return ResumeReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	rep := ResumeReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary: fmt.Sprintf("resume %s (%s, revision %d): %s", c.ID, c.Status, c.Revision, clipStr(c.Goal, 60))},
		Revision: c.Revision, Checkpoint: checkpoint, CheckpointFresh: fresh, Cursor: k.now().UTC(), EvidenceTotal: len(evidence),
	}
	if !in.Since.IsZero() {
		since := in.Since.UTC()
		rep.Since = &since
	}
	var selected []domain.Evidence
	for _, ev := range evidence {
		if ev.Sensitivity == domain.SensitivitySensitive {
			continue
		}
		if rep.Since != nil && !ev.Timestamp.After(*rep.Since) {
			continue
		}
		selected = append(selected, ev)
	}
	if rep.Since == nil && len(selected) > 10 {
		selected = selected[len(selected)-10:]
	}
	if len(selected) > limit {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("evidence delta bounded to the %d most recent of %d records", limit, len(selected)))
		selected = selected[len(selected)-limit:]
	}
	for _, ev := range selected {
		rep.Evidence = append(rep.Evidence, domain.ToFactView(ev))
	}
	if events, err := k.store.PhaseEvents(c.ID); err == nil {
		for _, e := range events {
			if rep.Since == nil || e.Timestamp.After(*rep.Since) {
				rep.PhaseEvents = append(rep.PhaseEvents, e)
			}
		}
		if rep.Since == nil && len(rep.PhaseEvents) > 5 {
			rep.PhaseEvents = rep.PhaseEvents[len(rep.PhaseEvents)-5:]
		}
	}
	if decisions, err := k.store.Decisions(c.ID); err == nil {
		for i := len(decisions) - 1; i >= 0; i-- {
			if decisions[i].Status == domain.DecisionPending {
				d := decisions[i]
				rep.PendingDecision = &d
				break
			}
		}
	}
	if findings, err := k.store.Findings(c.ID); err == nil {
		for _, f := range findings {
			if f.Status.Terminal() {
				continue
			}
			if rep.Since != nil && !f.UpdatedAt.After(*rep.Since) {
				continue
			}
			rep.Findings = append(rep.Findings, f)
			if len(rep.Findings) >= maxResumeFindings {
				break
			}
		}
	}
	if ledger := k.coverageFor(c.ID); ledger != nil {
		s := ledger.Summary()
		rep.Coverage = &s
	}
	if jobs, err := k.ListJobs(ctx, c.ID); err == nil {
		for _, j := range jobs.Jobs {
			if !j.Status.Terminal() || (rep.Since != nil && j.FinishedAt != nil && j.FinishedAt.After(*rep.Since)) {
				rep.Jobs = append(rep.Jobs, j)
			}
		}
	}
	stale, staleWarnings := k.staleEvidence(ctx, evidence, expectedEdits(k, c))
	rep.StaleEvidence = stale
	rep.Warnings = append(rep.Warnings, staleWarnings...)
	if len(stale) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d evidence record(s) describe files that changed since they were recorded — re-read before relying on them", len(stale)))
	}
	rep.NextActions = nextForPhase(c.Status)
	k.attachStructuredActions(&rep.Envelope, c)
	if ledger := k.coverageFor(c.ID); ledger != nil {
		rep.Actions = append(k.surveyActions(c, ledger), rep.Actions...)
	}
	k.redactEnvelope(&rep.Envelope)
	return rep, nil
}

func (k *Kernel) redactEnvelope(env *domain.Envelope) {
	env.Summary = k.red.String(env.Summary)
	for i := range env.Facts {
		env.Facts[i].Claim = k.red.String(env.Facts[i].Claim)
	}
	for i := range env.Warnings {
		env.Warnings[i] = k.red.String(env.Warnings[i])
	}
}
