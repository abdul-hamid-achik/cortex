package kernel

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// SessionView is a full, read-only picture of one session for `cortex show` —
// everything you'd want at a glance without running status + timeline + metrics
// separately. Central sessions are located by ID; ShowSessionIn additionally
// accepts a workspace fallback for repo-local/custom stores.
type SessionView struct {
	Slug                   string                      `json:"slug"`
	Case                   *domain.CaseFile            `json:"case"`
	Plan                   *domain.Plan                `json:"plan,omitempty"`
	Hypotheses             []domain.Hypothesis         `json:"hypotheses,omitempty"`
	Evidence               []domain.Evidence           `json:"evidence,omitempty"`
	Receipts               []domain.VerificationRecord `json:"receipts,omitempty"`
	Decisions              []domain.Decision           `json:"decisions,omitempty"`
	VerificationAssessment VerificationAssessment      `json:"verificationAssessment"`
	StaleVerification      []string                    `json:"staleVerification,omitempty"`
	VerificationWarnings   []string                    `json:"verificationWarnings,omitempty"`
	Actions                []domain.NextAction         `json:"actions,omitempty"`
	PhaseDurations         []PhaseDuration             `json:"phaseDurations,omitempty"`
	ElapsedMs              int64                       `json:"elapsedMs,omitempty"`
	Timeline               []TimelineEntry             `json:"timeline,omitempty"`
	EvidenceTotal          int                         `json:"evidenceTotal"`
	ReceiptTotal           int                         `json:"receiptTotal"`
	DecisionTotal          int                         `json:"decisionTotal"`
	TimelineTotal          int                         `json:"timelineTotal"`
	ProjectionWarnings     []string                    `json:"projectionWarnings,omitempty"`
	currentReceipts        []domain.VerificationRecord
}

const maxSessionViewLedgerRecords = 200

// ShowSession loads the full view of a session by ID from any repository.
func ShowSession(taskID string) (SessionView, error) {
	return ShowSessionIn("", taskID)
}

// ShowSessionIn loads a session with an explicit workspace fallback for
// repo-local/custom case stores. Central sessions remain globally locatable.
func ShowSessionIn(workspace, taskID string) (SessionView, error) {
	slug, store, err := LocateSessionIn(workspace, taskID)
	if err != nil {
		return SessionView{}, err
	}
	return sessionViewFromStore(slug, store, taskID)
}

// LoadSessionView loads the canonical session projection when the repository
// slug is already known (Studio uses this to avoid a second global tree walk).
func LoadSessionView(slug, taskID string) (SessionView, error) {
	store, err := casefs.New(filepath.Join(config.SessionsRoot(), slug))
	if err != nil {
		return SessionView{}, err
	}
	return sessionViewFromStore(slug, store, taskID)
}

func sessionViewFromStore(slug string, store *casefs.Store, taskID string) (SessionView, error) {
	snapshot, err := store.ViewSnapshot(taskID, maxSessionViewLedgerRecords)
	if err != nil {
		return SessionView{}, err
	}
	view := sessionViewFromSnapshot(slug, snapshot)
	// The full current receipt set is retained only for the handoff builder,
	// which calls sessionViewFromSnapshot directly. Show and Studio expose the
	// bounded projection and must not keep the full backing collection alive.
	view.currentReceipts = nil
	return view, nil
}

func sessionViewFromSnapshot(slug string, snapshot casefs.TaskSnapshot) SessionView {
	c := snapshot.Case
	currentRevision := adapters.Revision{}
	var revisionErr error
	if !c.Status.IsTerminal() {
		git := adapters.NewGit()
		currentRevision, revisionErr = git.CurrentRevision(context.Background(), c.Workspace.Root)
	}
	freshReceipts, staleReceipts := verificationReceiptsAtRevision(snapshot.Verifications, currentRevision, revisionErr)
	staleReceiptIDs := make([]string, 0, len(staleReceipts))
	for _, receipt := range staleReceipts {
		staleReceiptIDs = append(staleReceiptIDs, verificationReceiptDisplayID(receipt))
	}
	var verificationWarnings []string
	if revisionErr != nil {
		verificationWarnings = append(verificationWarnings, "could not check verification freshness: "+revisionErr.Error())
	}
	pd, elapsed := phaseDurations(c.CreatedAt, snapshot.PhaseEvents, c.Status.IsTerminal(), time.Now())
	assessment := assessCaseVerification(c, freshReceipts)
	actions := hydrateDecisionActions(c, structuredNextForCaseAt(c, time.Now().UTC(), assessment), snapshot.Decisions)
	view := SessionView{
		Slug: slug, Case: c, Plan: snapshot.Plan, Hypotheses: snapshot.Hypotheses, Evidence: snapshot.Evidence,
		Receipts: snapshot.Verifications, Decisions: snapshot.Decisions,
		VerificationAssessment: assessment,
		StaleVerification:      staleReceiptIDs,
		VerificationWarnings:   verificationWarnings,
		Actions:                actions,
		PhaseDurations:         pd, ElapsedMs: elapsed,
		Timeline:      timelineFromSnapshot(snapshot),
		EvidenceTotal: snapshot.EvidenceTotal, ReceiptTotal: snapshot.VerificationTotal,
		DecisionTotal: snapshot.DecisionTotal,
		TimelineTotal: snapshot.EvidenceTotal + snapshot.VerificationTotal + snapshot.PhaseTotal + snapshot.CommandTotal,
	}
	if snapshot.EvidenceTotal > len(snapshot.Evidence) {
		view.ProjectionWarnings = append(view.ProjectionWarnings, fmt.Sprintf("showing %d newest of %d evidence records", len(snapshot.Evidence), snapshot.EvidenceTotal))
	}
	redactSessionView(&view)
	// Handoffs need the complete current set for canonical assessment and their
	// own sensitivity/size budgets. Capture it after redaction, then bound the
	// human/model-facing Show and Studio collections independently.
	view.currentReceipts, _ = verificationReceiptsAtRevision(view.Receipts, currentRevision, revisionErr)
	view.Receipts = recentViewItems(view.Receipts, maxSessionViewLedgerRecords)
	view.Timeline = recentViewItems(view.Timeline, maxSessionViewLedgerRecords)
	if view.ReceiptTotal > len(view.Receipts) {
		view.ProjectionWarnings = append(view.ProjectionWarnings, fmt.Sprintf("showing %d newest of %d verification receipts", len(view.Receipts), view.ReceiptTotal))
	}
	if view.TimelineTotal > len(view.Timeline) {
		view.ProjectionWarnings = append(view.ProjectionWarnings, fmt.Sprintf("showing bounded recent activity; %d total timeline records remain available through cortex timeline", view.TimelineTotal))
	}
	return view
}

func recentViewItems[T any](items []T, limit int) []T {
	if limit <= 0 {
		return nil
	}
	if len(items) <= limit {
		return items
	}
	out := make([]T, limit)
	copy(out, items[len(items)-limit:])
	return out
}

// verificationReceiptDisplayID is stable for new records and keeps legacy
// records without IDs inspectable. UI stale markers compare this key rather
// than claim text so a fresh rerun of the same claim is never mislabeled stale.
func verificationReceiptDisplayID(receipt domain.VerificationRecord) string {
	if receipt.ID != "" {
		return receipt.ID
	}
	return receipt.Claim
}
