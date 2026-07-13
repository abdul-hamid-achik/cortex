package casefs

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestSnapshotNeverMixesVerificationTransactionFiles(t *testing.T) {
	root := t.TempDir()
	writer, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.CaseFile{ID: "task_snapshot", Status: domain.PhaseChanging}
	if err := writer.Create(c); err != nil {
		t.Fatal(err)
	}
	baseRevision := c.Revision
	c.Status = domain.PhaseVerifying

	var done atomic.Bool
	errs := make(chan error, 1)
	go func() {
		defer done.Store(true)
		for i := 0; i < 40; i++ {
			id := fmt.Sprintf("%02d", i)
			now := time.Now().UTC()
			evidence := domain.Evidence{
				ID: "ev_" + id, Timestamp: now, Kind: domain.KindCodeGraph,
				Source: domain.Source{Tool: "codemap"}, Claim: "fact " + id, Confidence: domain.ConfidenceHigh,
			}
			receipt := domain.VerificationRecord{
				ID: "vr_" + id, BatchID: "vb_" + id, Claim: "review " + id,
				Status: domain.VerifyPassed, Purpose: domain.VerificationPurposeVerifierRun,
				Binding: domain.VerificationBound, Evidence: []string{evidence.ID}, Timestamp: now,
			}
			for {
				err := writer.CommitVerificationBundle(c, []domain.Evidence{evidence}, []domain.VerificationRecord{receipt}, nil)
				if errors.Is(err, ErrBusy) {
					continue
				}
				if err != nil {
					errs <- err
					return
				}
				break
			}
		}
	}()

	for !done.Load() {
		snapshot, err := reader.Snapshot(c.ID)
		if errors.Is(err, ErrBusy) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Evidence) != len(snapshot.Verifications) {
			t.Fatalf("mixed snapshot: %d evidence, %d receipts", len(snapshot.Evidence), len(snapshot.Verifications))
		}
		wantRevision := baseRevision + uint64(len(snapshot.Verifications))
		if snapshot.Case.Revision != wantRevision {
			t.Fatalf("mixed case anchor: revision=%d want=%d receipts=%d", snapshot.Case.Revision, wantRevision, len(snapshot.Verifications))
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	final, err := reader.Snapshot(c.ID)
	if err != nil || len(final.Evidence) != 40 || len(final.Verifications) != 40 {
		t.Fatalf("final snapshot = evidence %d receipts %d err %v", len(final.Evidence), len(final.Verifications), err)
	}
}

func TestBoundedSnapshotsStreamEvidenceInsteadOfReturningWholeLedger(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.CaseFile{ID: "task_bounded_snapshot", Status: domain.PhaseInvestigating}
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 35; i++ {
		sensitivity := domain.SensitivityNormal
		if i%7 == 0 {
			sensitivity = domain.SensitivitySensitive
		}
		if err := store.AppendEvidence(c.ID, domain.Evidence{
			ID: fmt.Sprintf("ev_%02d", i), Timestamp: time.Now().UTC(), Kind: domain.KindHumanReport,
			Source: domain.Source{Origin: "human"}, Claim: fmt.Sprintf("fact %02d", i),
			Confidence: domain.ConfidenceMedium, Sensitivity: sensitivity,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendCommand(c.ID, CommandRecord{Timestamp: time.Now().UTC(), Tool: "tool", Operation: fmt.Sprintf("op_%02d", i), Status: "ok"}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendPhaseEvent(c.ID, PhaseEvent{Timestamp: time.Now().UTC(), From: domain.PhaseInvestigating, To: domain.PhaseInvestigating}); err != nil {
			t.Fatal(err)
		}
	}
	handoff, err := store.HandoffSnapshot(c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.EvidenceTotal != 35 || handoff.ShareableEvidenceTotal != 30 || handoff.SensitiveEvidenceOmitted != 5 || len(handoff.Evidence) != 10 {
		t.Fatalf("handoff counts = total %d shareable %d sensitive %d retained %d", handoff.EvidenceTotal, handoff.ShareableEvidenceTotal, handoff.SensitiveEvidenceOmitted, len(handoff.Evidence))
	}
	for _, item := range handoff.Evidence {
		if item.Sensitivity == domain.SensitivitySensitive {
			t.Fatalf("sensitive item retained: %+v", item)
		}
	}
	completion, err := store.CompletionSnapshot(c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if completion.EvidenceTotal != 35 || completion.ShareableEvidenceTotal != 30 || completion.SensitiveEvidenceOmitted != 5 || len(completion.Evidence) != 10 {
		t.Fatalf("completion counts = total %d shareable %d sensitive %d retained %d", completion.EvidenceTotal, completion.ShareableEvidenceTotal, completion.SensitiveEvidenceOmitted, len(completion.Evidence))
	}
	for _, item := range completion.Evidence {
		if item.Sensitivity == domain.SensitivitySensitive {
			t.Fatalf("sensitive item retained for completion: %+v", item)
		}
	}
	status, err := store.StatusSnapshot(c.ID)
	if err != nil || status.EvidenceTotal != 35 || len(status.Evidence) != 0 {
		t.Fatalf("status snapshot retained ledger: count=%d retained=%d err=%v", status.EvidenceTotal, len(status.Evidence), err)
	}
	view, err := store.ViewSnapshot(c.ID, 10)
	if err != nil || view.EvidenceTotal != 35 || view.CommandTotal != 35 || view.PhaseTotal != 35 ||
		len(view.Evidence) != 10 || len(view.Commands) != 10 || len(view.PhaseEvents) != 10 {
		t.Fatalf("view snapshot is not bounded: evidence=%d/%d commands=%d/%d phases=%d/%d err=%v",
			len(view.Evidence), view.EvidenceTotal, len(view.Commands), view.CommandTotal,
			len(view.PhaseEvents), view.PhaseTotal, err)
	}
}
