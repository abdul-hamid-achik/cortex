package kernel

import (
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// SessionView is a full, read-only picture of one session for `cortex show` —
// everything you'd want at a glance without running status + timeline + metrics
// separately. Workspace-independent: the session is located by ID anywhere in
// the central tree.
type SessionView struct {
	Slug           string                      `json:"slug"`
	Case           *domain.CaseFile            `json:"case"`
	Hypotheses     []domain.Hypothesis         `json:"hypotheses,omitempty"`
	Receipts       []domain.VerificationRecord `json:"receipts,omitempty"`
	PhaseDurations []PhaseDuration             `json:"phaseDurations,omitempty"`
	ElapsedMs      int64                       `json:"elapsedMs,omitempty"`
	Timeline       []TimelineEntry             `json:"timeline,omitempty"`
}

// ShowSession loads the full view of a session by ID from any repository.
func ShowSession(taskID string) (SessionView, error) {
	slug, store, err := LocateSession(taskID)
	if err != nil {
		return SessionView{}, err
	}
	c, err := store.Load(taskID)
	if err != nil {
		return SessionView{}, err
	}
	hyps, _ := store.Hypotheses(taskID)
	recs, _ := store.Verifications(taskID)
	events, _ := store.PhaseEvents(taskID)
	pd, elapsed := phaseDurations(c.CreatedAt, events, c.Status.IsTerminal(), time.Now())
	return SessionView{
		Slug:           slug,
		Case:           c,
		Hypotheses:     hyps,
		Receipts:       recs,
		PhaseDurations: pd,
		ElapsedMs:      elapsed,
		Timeline:       timelineFromStore(store, taskID),
	}, nil
}
