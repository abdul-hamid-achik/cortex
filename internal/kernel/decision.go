package kernel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// RequestDecisionInput pauses a task on a bounded question for a human.
type RequestDecisionInput struct {
	TaskID    string
	Question  string
	Options   []domain.DecisionOption
	Requester string
}

// AnswerDecisionInput records the selected option and the human who chose it.
type AnswerDecisionInput struct {
	TaskID     string
	DecisionID string
	Answer     string // DecisionOption.ID
	Responder  string
}

// RequestDecision persists a pending decision and moves the case into the
// resumable needs_human_decision state. PausedFrom is recorded before the case is
// saved so AnswerDecision can return to the exact phase that was interrupted.
func (k *Kernel) RequestDecision(in RequestDecisionInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	pending, err := k.pendingDecision(c.ID)
	if err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	if pending != nil {
		paused, pauseErr := k.ensureDecisionPaused(c.ID)
		if pauseErr != nil {
			return errEnvelope(c.ID, pauseErr.Error()), pauseErr
		}
		return k.decisionRequestEnvelope(paused, *pending, "recovered existing pending decision after an interrupted request"), nil
	}
	if c.Status == domain.PhaseNeedsHumanDecision {
		return errEnvelope(c.ID, "task is waiting for a human decision but has no pending decision record"), nil
	}
	if c.Status.IsTerminal() {
		return errEnvelope(c.ID, fmt.Sprintf("cannot request a decision in terminal phase %q", c.Status)), nil
	}
	if c.PausedFrom != "" {
		return errEnvelope(c.ID, fmt.Sprintf("cannot request a decision: stale pausedFrom %q is already set", c.PausedFrom)), nil
	}
	if !domain.CanTransition(c.Status, domain.PhaseNeedsHumanDecision) {
		return errEnvelope(c.ID, fmt.Sprintf("cannot request a decision in phase %q", c.Status)), nil
	}

	decision, err := k.buildDecision(in)
	if err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	if err := k.store.AppendDecision(c.ID, decision); err != nil {
		// Another requester may have won the decisions.json race, or a prior call
		// may have lost its response after appending. Coalesce onto that durable
		// pending request instead of leaving the case active and unanswerable.
		pending, pendingErr := k.pendingDecision(c.ID)
		if pendingErr == nil && pending != nil {
			paused, pauseErr := k.ensureDecisionPaused(c.ID)
			if pauseErr != nil {
				return errEnvelope(c.ID, pauseErr.Error()), pauseErr
			}
			return k.decisionRequestEnvelope(paused, *pending, "another concurrent request established the pending decision"), nil
		}
		return errEnvelope(c.ID, err.Error()), err
	}
	paused, err := k.ensureDecisionPaused(c.ID)
	if err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	return k.decisionRequestEnvelope(paused, decision, ""), nil
}

func (k *Kernel) pendingDecision(taskID string) (*domain.Decision, error) {
	decisions, err := k.store.Decisions(taskID)
	if err != nil {
		return nil, err
	}
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			decision := decisions[i]
			return &decision, nil
		}
	}
	return nil, nil
}

// ensureDecisionPaused repairs the only non-atomic edge in a decision request:
// decisions.json may have landed before case.json. The CAS loop pauses the
// latest active phase and records history only after the snapshot write wins.
func (k *Kernel) ensureDecisionPaused(taskID string) (*domain.CaseFile, error) {
	for attempt := 0; attempt < maxLeaseCASAttempts; attempt++ {
		c, err := k.store.Load(taskID)
		if err != nil {
			return nil, err
		}
		if c.Status == domain.PhaseNeedsHumanDecision {
			if err := validateResumeTarget(c.PausedFrom); err != nil {
				return nil, err
			}
			return c, nil
		}
		if c.Status.IsTerminal() {
			return nil, fmt.Errorf("cannot request a decision in terminal phase %q", c.Status)
		}
		if c.PausedFrom != "" {
			return nil, fmt.Errorf("cannot request a decision: stale pausedFrom %q is already set", c.PausedFrom)
		}
		if !domain.CanTransition(c.Status, domain.PhaseNeedsHumanDecision) {
			return nil, fmt.Errorf("cannot request a decision in phase %q", c.Status)
		}
		from := c.Status
		c.PausedFrom = from
		c.Status = domain.PhaseNeedsHumanDecision
		if err := k.store.Save(c); err != nil {
			if errors.Is(err, casefs.ErrRevisionConflict) {
				continue
			}
			return nil, err
		}
		k.recordPhase(c.ID, from, c.Status)
		return c, nil
	}
	return nil, fmt.Errorf("case changed concurrently too many times; retry decision request")
}

func (k *Kernel) decisionRequestEnvelope(c *domain.CaseFile, decision domain.Decision, warning string) domain.Envelope {
	env := domain.Envelope{
		OK: true, TaskID: c.ID, Phase: c.Status,
		Summary: fmt.Sprintf("waiting for decision %s: %s", decision.ID, decision.Question),
		Artifacts: []domain.ArtifactRef{{
			ID: decision.ID, Kind: "decision", URI: fmt.Sprintf("case://%s/decisions/%s", c.ID, decision.ID),
			Summary: decision.Question,
		}},
		NextActions:  []string{"answer the pending decision before continuing the task"},
		RawAvailable: false,
	}
	if warning != "" {
		env.Warnings = []string{warning}
	}
	k.attachStructuredActions(&env, c)
	env.Actions = bindPendingDecision(env.Actions, []domain.Decision{decision})
	return env
}

// AnswerDecision atomically changes a pending decision to answered, appends a
// redacted human_report evidence record, and resumes the case exactly PausedFrom.
func (k *Kernel) AnswerDecision(in AnswerDecisionInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	answer := strings.TrimSpace(in.Answer)
	if answer == "" {
		return errEnvelope(c.ID, "decision answer needs an option id"), nil
	}
	if textExceeds(answer, maxStableIdentifierBytes) || textExceeds(strings.TrimSpace(in.DecisionID), maxStableIdentifierBytes) {
		return errEnvelope(c.ID, fmt.Sprintf("decision and answer ids must be at most %d bytes", maxStableIdentifierBytes)), nil
	}
	if k.red.Detected(answer) {
		return errEnvelope(c.ID, "decision answer must use a non-sensitive option id"), nil
	}
	responderRaw := strings.TrimSpace(in.Responder)
	if responderRaw == "" {
		return errEnvelope(c.ID, "decision answer needs a responder"), nil
	}
	if textExceeds(responderRaw, maxStableIdentifierBytes) {
		return errEnvelope(c.ID, fmt.Sprintf("decision responder exceeds %d bytes", maxStableIdentifierBytes)), nil
	}
	responder := k.red.String(responderRaw)
	if c.Status != domain.PhaseNeedsHumanDecision {
		answered, decisionErr := k.store.Decision(c.ID, in.DecisionID)
		if decisionErr == nil && answered.Status == domain.DecisionAnswered && answered.Answer == answer {
			ev, evidenceErr := k.ensureDecisionEvidence(c.ID, answered)
			if evidenceErr != nil {
				return errEnvelope(c.ID, evidenceErr.Error()), evidenceErr
			}
			return k.decisionAnsweredEnvelope(c, answered, ev, true), nil
		}
		return errEnvelope(c.ID, fmt.Sprintf("cannot answer a decision in phase %q", c.Status)), nil
	}
	if err := validateResumeTarget(c.PausedFrom); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	evidenceID := ids.New("ev")
	answered, err := k.store.AnswerDecision(
		c.ID, in.DecisionID, answer, responder, evidenceID, k.now().UTC(), k.red.Detected(responderRaw),
	)
	if err != nil {
		// Response loss after decisions.json was written is safe to retry. Resume
		// the already-recorded identical answer; never overwrite a different one.
		existing, decisionErr := k.store.Decision(c.ID, in.DecisionID)
		if decisionErr != nil || existing.Status != domain.DecisionAnswered || existing.Answer != answer {
			return errEnvelope(c.ID, err.Error()), nil
		}
		answered = existing
	}
	ev, err := k.ensureDecisionEvidence(c.ID, answered)
	if err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	return k.resumeDecision(c, answered, ev)
}

// ResumeDecision recovers an answered decision whose case could not be resumed
// after the answer write (for example, a process stopped between the two atomic
// file updates). It refuses to bypass a still-pending human decision.
func (k *Kernel) ResumeDecision(taskID string) (domain.Envelope, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return errEnvelope(taskID, err.Error()), nil
	}
	if c.Status != domain.PhaseNeedsHumanDecision {
		return errEnvelope(c.ID, fmt.Sprintf("cannot resume a decision in phase %q", c.Status)), nil
	}
	if err := validateResumeTarget(c.PausedFrom); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	decisions, err := k.store.Decisions(c.ID)
	if err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			return errEnvelope(c.ID, fmt.Sprintf("decision %s is still pending", decisions[i].ID)), nil
		}
		if decisions[i].Status == domain.DecisionAnswered {
			ev, err := k.ensureDecisionEvidence(c.ID, decisions[i])
			if err != nil {
				return errEnvelope(c.ID, err.Error()), err
			}
			return k.resumeDecision(c, decisions[i], ev)
		}
	}
	return errEnvelope(c.ID, "cannot resume: no answered decision exists"), nil
}

func (k *Kernel) buildDecision(in RequestDecisionInput) (domain.Decision, error) {
	questionRaw := strings.TrimSpace(in.Question)
	requesterRaw := strings.TrimSpace(in.Requester)
	if textExceeds(questionRaw, maxRecordTextBytes) {
		return domain.Decision{}, fmt.Errorf("decision question exceeds %d bytes", maxRecordTextBytes)
	}
	if textExceeds(requesterRaw, maxStableIdentifierBytes) {
		return domain.Decision{}, fmt.Errorf("decision requester exceeds %d bytes", maxStableIdentifierBytes)
	}
	if len(in.Options) > maxDecisionOptions {
		return domain.Decision{}, fmt.Errorf("decision has more than %d options", maxDecisionOptions)
	}
	decision := domain.Decision{
		ID: ids.New("dec"), Question: k.red.String(questionRaw), Requester: k.red.String(requesterRaw),
		RequestedAt: k.now().UTC(), Status: domain.DecisionPending,
	}
	decision.Sensitive = k.red.Detected(questionRaw) || k.red.Detected(requesterRaw)
	decision.Options = make([]domain.DecisionOption, 0, len(in.Options))
	for _, raw := range in.Options {
		if textExceeds(strings.TrimSpace(raw.ID), maxStableIdentifierBytes) ||
			textExceeds(strings.TrimSpace(raw.Label), maxLocatorBytes) ||
			textExceeds(strings.TrimSpace(raw.Consequence), maxRecordTextBytes) {
			return domain.Decision{}, fmt.Errorf("decision option id, label, and consequence exceed their size limits")
		}
		option := domain.DecisionOption{
			ID: strings.TrimSpace(raw.ID), Label: k.red.String(strings.TrimSpace(raw.Label)),
			Consequence: k.red.String(strings.TrimSpace(raw.Consequence)),
		}
		if k.red.Detected(option.ID) {
			return domain.Decision{}, fmt.Errorf("decision option id looks sensitive; use a non-sensitive stable id")
		}
		decision.Sensitive = decision.Sensitive || k.red.Detected(raw.Label) || k.red.Detected(raw.Consequence)
		decision.Options = append(decision.Options, option)
	}
	if err := decision.Validate(); err != nil {
		return domain.Decision{}, err
	}
	return decision, nil
}

func validateResumeTarget(target domain.Phase) error {
	if target == "" || target == domain.PhaseNeedsHumanDecision || target.IsTerminal() || !domain.CanTransition(domain.PhaseNeedsHumanDecision, target) {
		return fmt.Errorf("cannot resume: pausedFrom %q is not an active lifecycle phase", target)
	}
	return nil
}

func (k *Kernel) ensureDecisionEvidence(taskID string, decision domain.Decision) (domain.Evidence, error) {
	option, ok := decision.Option(decision.Answer)
	if !ok || decision.AnsweredAt == nil {
		return domain.Evidence{}, fmt.Errorf("answered decision %s is incomplete", decision.ID)
	}
	claim := k.red.String(fmt.Sprintf(
		"decision %s answered by %s: %s -> %s (%s); consequence: %s",
		decision.ID, decision.Responder, decision.Question, option.Label, option.ID, option.Consequence,
	))
	ev := domain.Evidence{
		ID: decision.EvidenceID, Timestamp: decision.AnsweredAt.UTC(), Kind: domain.KindHumanReport,
		Source: domain.Source{
			Origin: "human", Actor: decision.Responder,
			URI: fmt.Sprintf("case://%s/decisions/%s", taskID, decision.ID),
		},
		Claim: claim, Category: "decision", Confidence: domain.ConfidenceMedium,
		Sensitivity: sensitivity(decision.Sensitive || k.red.Detected(claim)),
		RawRef:      fmt.Sprintf("case://%s/evidence/%s", taskID, decision.EvidenceID),
	}
	durable, _, err := k.store.AppendEvidenceOnce(taskID, ev)
	if err != nil {
		return domain.Evidence{}, err
	}
	if !sameDecisionEvidence(durable, ev) {
		return domain.Evidence{}, fmt.Errorf("evidence id %s already belongs to a different record", ev.ID)
	}
	return durable, nil
}

func sameDecisionEvidence(a, b domain.Evidence) bool {
	return a.ID == b.ID && a.Timestamp.Equal(b.Timestamp) && a.Kind == b.Kind &&
		a.Source.Origin == b.Source.Origin && a.Source.Actor == b.Source.Actor && a.Source.URI == b.Source.URI &&
		a.Claim == b.Claim && a.Category == b.Category && a.Confidence == b.Confidence &&
		a.Sensitivity == b.Sensitivity && a.RawRef == b.RawRef
}

func (k *Kernel) resumeDecision(c *domain.CaseFile, decision domain.Decision, ev domain.Evidence) (domain.Envelope, error) {
	for attempt := 0; attempt < maxLeaseCASAttempts; attempt++ {
		latest, err := k.store.Load(c.ID)
		if err != nil {
			return errEnvelope(c.ID, err.Error()), err
		}
		if latest.Status != domain.PhaseNeedsHumanDecision {
			if latest.PausedFrom == "" {
				return k.decisionAnsweredEnvelope(latest, decision, ev, true), nil
			}
			return errEnvelope(c.ID, fmt.Sprintf("cannot resume a decision in phase %q", latest.Status)), nil
		}
		target := latest.PausedFrom
		if err := validateResumeTarget(target); err != nil {
			return errEnvelope(c.ID, err.Error()), nil
		}
		from := latest.Status
		latest.Status = target
		latest.PausedFrom = ""
		if err := k.store.Save(latest); err != nil {
			if errors.Is(err, casefs.ErrRevisionConflict) {
				continue
			}
			return errEnvelope(c.ID, err.Error()), err
		}
		k.recordPhase(latest.ID, from, target)
		return k.decisionAnsweredEnvelope(latest, decision, ev, false), nil
	}
	return errEnvelope(c.ID, "case changed concurrently too many times; retry decision resume"), nil
}

func (k *Kernel) decisionAnsweredEnvelope(c *domain.CaseFile, decision domain.Decision, ev domain.Evidence, already bool) domain.Envelope {
	summary := fmt.Sprintf("decision %s answered; resumed %s", decision.ID, c.Status)
	if already {
		summary = fmt.Sprintf("decision %s was already answered; task is %s", decision.ID, c.Status)
	}
	env := domain.Envelope{
		OK: true, TaskID: c.ID, Phase: c.Status,
		Summary:     summary,
		Facts:       []domain.FactView{domain.ToFactView(ev)},
		NextActions: nextForPhase(c.Status), RawAvailable: false,
	}
	k.attachStructuredActions(&env, c)
	return env
}
