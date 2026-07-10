package kernel

import (
	"context"
	"fmt"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
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
	// A completed/abandoned/blocked task is immutable — its summary and durable
	// memory are already written, so hypotheses/evidence must not diverge from
	// them post-hoc (mirrors AbortTask's terminal guard).
	if c.Status.IsTerminal() {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot resolve a hypothesis in terminal phase %q", c.Status)), nil
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

	hyps, err := k.store.Hypotheses(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	idx := -1
	for i, h := range hyps {
		if h.ID == in.HypothesisID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errEnvelope(in.TaskID, "no hypothesis "+in.HypothesisID+" in this task"), nil
	}
	if hyps[idx].Status == status {
		return errEnvelope(in.TaskID, fmt.Sprintf("hypothesis %s is already %s", in.HypothesisID, status)), nil
	}
	// Cited evidence must exist in this case's ledger — provenance can't reference
	// phantom records.
	for _, evID := range in.Evidence {
		if _, err := k.store.GetEvidence(in.TaskID, evID); err != nil {
			return errEnvelope(in.TaskID, "no evidence "+evID+" in this task to cite"), nil
		}
	}

	prev := hyps[idx].Status
	hyps[idx].Status = status
	// Confirmation raises confidence to high; rejection drops it to low. A
	// challenge leaves confidence but flags the hypothesis for revision.
	switch status {
	case domain.HypConfirmed:
		hyps[idx].Confidence = domain.ConfidenceHigh
	case domain.HypRejected:
		hyps[idx].Confidence = domain.ConfidenceLow
	}

	// Append the resolution as evidence so the ledger records WHY the status
	// changed, citing the evidence that drove it (contradiction handling keeps
	// history, per SPEC §9.3). Stamped before saving hypotheses so an
	// auto-minted record's own ID can be cited as this confirmation's support.
	claim := fmt.Sprintf("hypothesis %s %s (was %s): %s", in.HypothesisID, status, prev, in.Reason)
	if len(in.Evidence) > 0 {
		claim += " [evidence: " + joinStr(in.Evidence, ", ") + "]"
	} else if autoEvidence {
		claim += " [evidence: auto-recorded from this reason; no prior evidence record was cited]"
	}
	ev, err := k.stampEvidence(in.TaskID, "human", adapters.Fact{
		Kind: "human_report", Claim: claim, Confidence: confidenceForResolution(status),
	})
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), err
	}

	if status == domain.HypConfirmed {
		supporting := in.Evidence
		if autoEvidence {
			supporting = []string{ev.ID}
		}
		hyps[idx].Supports = dedupeStr(append(hyps[idx].Supports, supporting...))
	}
	if err := k.store.SaveHypotheses(in.TaskID, hyps); err != nil {
		return errEnvelope(in.TaskID, err.Error()), err
	}

	// Cross-case disproof recall (SPEC §15.4): index the resolved hypothesis
	// immediately — rejected/challenged are the gold. Best-effort, decoupled
	// from this request's lifecycle (a cancelled caller must not drop a save
	// that already landed), so a background context is correct here.
	k.indexResolvedHypothesis(context.Background(), c, hyps[idx], in.Reason)

	env := domain.Envelope{
		OK:      true,
		TaskID:  c.ID,
		Phase:   c.Status,
		Summary: claim,
		Facts:   []domain.FactView{domain.ToFactView(ev)},
	}
	for _, h := range hyps {
		env.Hypotheses = append(env.Hypotheses, domain.ToHypView(h))
	}
	if status == domain.HypRejected || status == domain.HypChallenged {
		env.NextActions = []string{"revise the hypothesis or investigate further before planning a change"}
	}
	return env, nil
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
