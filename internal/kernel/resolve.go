package kernel

import (
	"context"
	"errors"
	"fmt"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// ResolveInput parameterizes Resolve (SPEC §9.3 contradiction handling).
type ResolveInput struct {
	TaskID       string
	HypothesisID string
	Status       string   // confirmed | challenged | rejected
	Reason       string   // why the status changed
	Evidence     []string // supporting/contradicting evidence IDs
}

// Resolve updates a hypothesis's status as evidence accumulates, preserving the
// investigation path rather than silently overwriting it (SPEC §9.3): the prior
// hypothesis is retained, its status is changed, and a provenance-bearing
// evidence record documenting the resolution is appended. A confirmed
// hypothesis needs supporting evidence; a challenge/rejection records the
// contradicting evidence.
func (k *Kernel) Resolve(in ResolveInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if message := resolvePhaseError(c); message != "" {
		return errEnvelope(in.TaskID, message), nil
	}
	status, ok := parseHypStatus(in.Status)
	if !ok {
		return errEnvelope(in.TaskID, "status must be one of: confirmed, challenged, rejected"), nil
	}
	if in.Reason == "" {
		return errEnvelope(in.TaskID, "resolve needs a reason (what evidence changed the status)"), nil
	}
	// Confirmation raises a hypothesis to high confidence, so it must rest on
	// evidence — a hypothesis can't be promoted on assertion alone (SPEC §5.2
	// evidence-not-assertions, §9.3). That evidence need not already have a
	// formal evidence ID: when none is cited, the reason itself is minted into
	// an evidence record below and cited automatically, so a caller whose proof
	// was e.g. an ad hoc repro script (no cortex_investigate evidence ID) isn't
	// forced to skip resolve entirely (dogfooding 2026-07-07).
	autoEvidence := status == domain.HypConfirmed && len(in.Evidence) == 0

	// Cited evidence must exist in this case's ledger — provenance can't reference
	// phantom records.
	for _, evID := range in.Evidence {
		if _, err := k.store.GetEvidence(in.TaskID, evID); err != nil {
			return errEnvelope(in.TaskID, "no evidence "+evID+" in this task to cite"), nil
		}
	}

	// Mint one stable evidence identity before the CAS loop. If another writer
	// advances the task, the retry reapplies this bounded resolution to the
	// latest hypotheses snapshot without duplicating evidence.
	evidenceID := ids.New("ev")
	evidenceAt := k.now().UTC()
	var (
		hyps     []domain.Hypothesis
		ev       *domain.Evidence
		resolved domain.Hypothesis
	)
	for attempt := 0; attempt < maxLeaseCASAttempts; attempt++ {
		if attempt > 0 {
			c, err = k.store.Load(in.TaskID)
			if err != nil {
				return errEnvelope(in.TaskID, err.Error()), nil
			}
			if message := resolvePhaseError(c); message != "" {
				return errEnvelope(in.TaskID, message), nil
			}
		}

		hyps, ev, err = k.store.UpdateHypotheses(c, func(current []domain.Hypothesis) ([]domain.Hypothesis, *domain.Evidence, error) {
			idx := -1
			for i := range current {
				if current[i].ID == in.HypothesisID {
					idx = i
					break
				}
			}
			if idx < 0 {
				return nil, nil, resolveRuleError("no hypothesis " + in.HypothesisID + " in this task")
			}
			if current[idx].Status == status {
				return nil, nil, resolveRuleError(fmt.Sprintf("hypothesis %s is already %s", in.HypothesisID, status))
			}

			prev := current[idx].Status
			current[idx].Status = status
			// Confirmation raises confidence to high; rejection drops it to low. A
			// challenge leaves confidence but flags the hypothesis for revision.
			switch status {
			case domain.HypConfirmed:
				current[idx].Confidence = domain.ConfidenceHigh
			case domain.HypRejected:
				current[idx].Confidence = domain.ConfidenceLow
			}

			claim := fmt.Sprintf("hypothesis %s %s (was %s): %s", in.HypothesisID, status, prev, in.Reason)
			if len(in.Evidence) > 0 {
				claim += " [evidence: " + joinStr(in.Evidence, ", ") + "]"
			} else if autoEvidence {
				claim += " [evidence: auto-recorded from this reason; no prior evidence record was cited]"
			}
			resolutionEvidence := &domain.Evidence{
				ID:          evidenceID,
				Timestamp:   evidenceAt,
				Kind:        domain.KindHumanReport,
				Source:      domain.Source{Tool: "human"},
				Claim:       k.red.String(claim),
				Confidence:  mapConfidence(confidenceForResolution(status)),
				Sensitivity: sensitivity(k.red.Detected(claim)),
				RawRef:      fmt.Sprintf("case://%s/evidence/%s", in.TaskID, evidenceID),
			}
			if status == domain.HypConfirmed {
				supporting := in.Evidence
				if autoEvidence {
					supporting = []string{evidenceID}
				}
				current[idx].Supports = dedupeStr(append(current[idx].Supports, supporting...))
			}
			resolved = current[idx]
			return current, resolutionEvidence, nil
		})
		if err == nil {
			break
		}
		if errors.Is(err, casefs.ErrRevisionConflict) {
			continue
		}
		var ruleErr resolveRuleError
		if errors.As(err, &ruleErr) {
			return errEnvelope(in.TaskID, ruleErr.Error()), nil
		}
		return errEnvelope(in.TaskID, err.Error()), err
	}
	if err != nil {
		// Preserve the store's typed retryable conflict so callers can reload and
		// retry rather than parsing an opaque "changed concurrently" string.
		return errEnvelope(in.TaskID, err.Error()), err
	}

	// Cross-case disproof recall (SPEC §15.4): index the resolved hypothesis
	// immediately — rejected/challenged are the gold. Best-effort, decoupled
	// from this request's lifecycle (a cancelled caller must not drop a save
	// that already landed), so a background context is correct here.
	k.indexResolvedHypothesis(context.Background(), c, resolved, in.Reason)

	env := domain.Envelope{
		OK:      true,
		TaskID:  c.ID,
		Phase:   c.Status,
		Summary: ev.Claim,
		Facts:   []domain.FactView{domain.ToFactView(*ev)},
	}
	for _, h := range hyps {
		env.Hypotheses = append(env.Hypotheses, domain.ToHypView(h))
	}
	if status == domain.HypRejected || status == domain.HypChallenged {
		env.NextActions = []string{"revise the hypothesis or investigate further before planning a change"}
	}
	k.attachStructuredActions(&env, c)
	return env, nil
}

type resolveRuleError string

func (e resolveRuleError) Error() string { return string(e) }

func resolvePhaseError(c *domain.CaseFile) string {
	// A completed/abandoned/blocked task is immutable — its summary and durable
	// memory are already written, so hypotheses/evidence must not diverge from
	// them post-hoc (mirrors AbortTask's terminal guard).
	if c.Status.IsTerminal() {
		return fmt.Sprintf("cannot resolve a hypothesis in terminal phase %q", c.Status)
	}
	if c.Status == domain.PhaseNeedsHumanDecision {
		return "cannot resolve a hypothesis while waiting for a human decision"
	}
	return ""
}

func parseHypStatus(s string) (domain.HypothesisStatus, bool) {
	switch s {
	case "confirmed":
		return domain.HypConfirmed, true
	case "challenged":
		return domain.HypChallenged, true
	case "rejected":
		return domain.HypRejected, true
	default:
		return "", false
	}
}

func confidenceForResolution(s domain.HypothesisStatus) string {
	switch s {
	case domain.HypConfirmed:
		return "high"
	case domain.HypRejected:
		return "low"
	default:
		return "medium"
	}
}
