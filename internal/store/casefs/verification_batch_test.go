package casefs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestAppendVerificationBatchIsAllOrNothing(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	caseFile := &domain.CaseFile{ID: "task_batch"}
	if err := store.Create(caseFile); err != nil {
		t.Fatal(err)
	}
	valid := domain.VerificationRecord{
		ID: "vr_one", BatchID: "vb_one", Claim: "structural review", Status: domain.VerifyPassed,
		Purpose: domain.VerificationPurposeVerifierRun, Binding: domain.VerificationBound,
	}
	invalid := domain.VerificationRecord{
		ID: "vr_two", BatchID: "vb_two", Claim: "named claim", Status: domain.VerifyPassed,
		Purpose: domain.VerificationPurposeNamedClaim, Binding: domain.VerificationBound,
	}
	if err := store.AppendVerificationBatch(caseFile.ID, []domain.VerificationRecord{valid, invalid}); err == nil {
		t.Fatal("mixed batch ids should be rejected")
	}
	records, err := store.Verifications(caseFile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("invalid batch was partially appended: %+v", records)
	}

	invalid.BatchID = valid.BatchID
	if err := store.AppendVerificationBatch(caseFile.ID, []domain.VerificationRecord{valid, invalid}); err != nil {
		t.Fatal(err)
	}
	records, err = store.Verifications(caseFile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].BatchID != records[1].BatchID {
		t.Fatalf("valid batch was not committed together: %+v", records)
	}
}

func TestCommitVerificationBundleIsRevisionGuardedAndAtomic(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	caseFile := &domain.CaseFile{ID: "task_bundle", Status: domain.PhaseChanging}
	if err := store.Create(caseFile); err != nil {
		t.Fatal(err)
	}
	stale := *caseFile
	caseFile.Status = domain.PhaseVerifying
	now := time.Now().UTC()
	evidence := domain.Evidence{
		ID: "ev_bundle", Timestamp: now, Kind: domain.KindCodeGraph,
		Source: domain.Source{Tool: "codemap"}, Claim: "bundle fact", Confidence: domain.ConfidenceHigh,
		RawRef: "case://task_bundle/raw/raw_bundle",
	}
	receipt := domain.VerificationRecord{
		ID: "vr_bundle", BatchID: "vb_bundle", Claim: "bundle review", Status: domain.VerifyPassed,
		Purpose: domain.VerificationPurposeVerifierRun, Binding: domain.VerificationBound,
		Evidence: []string{evidence.ID}, Timestamp: now,
	}
	if err := store.CommitVerificationBundle(caseFile, []domain.Evidence{evidence}, []domain.VerificationRecord{receipt}, []RawRecord{{ID: "raw_bundle", Content: "raw proof"}}); err != nil {
		t.Fatal(err)
	}
	if caseFile.Revision <= stale.Revision {
		t.Fatalf("bundle did not advance case revision: stale=%d current=%d", stale.Revision, caseFile.Revision)
	}
	gotEvidence, _ := store.Evidence(caseFile.ID)
	gotReceipts, _ := store.Verifications(caseFile.ID)
	gotRaw, rawErr := store.ReadRaw(caseFile.ID, "raw_bundle")
	if len(gotEvidence) != 1 || len(gotReceipts) != 1 || gotRaw != "raw proof" || rawErr != nil {
		t.Fatalf("bundle was not fully published: evidence=%+v receipts=%+v raw=%q err=%v", gotEvidence, gotReceipts, gotRaw, rawErr)
	}

	stale.Status = domain.PhaseVerifying
	losingEvidence := evidence
	losingEvidence.ID = "ev_loser"
	losingReceipt := receipt
	losingReceipt.ID, losingReceipt.BatchID, losingReceipt.Evidence = "vr_loser", "vb_loser", []string{losingEvidence.ID}
	err = store.CommitVerificationBundle(&stale, []domain.Evidence{losingEvidence}, []domain.VerificationRecord{losingReceipt}, []RawRecord{{ID: "raw_loser", Content: "must not land"}})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale bundle error = %v, want revision conflict", err)
	}
	gotEvidence, _ = store.Evidence(caseFile.ID)
	gotReceipts, _ = store.Verifications(caseFile.ID)
	if len(gotEvidence) != 1 || len(gotReceipts) != 1 {
		t.Fatalf("losing bundle leaked records: evidence=%+v receipts=%+v", gotEvidence, gotReceipts)
	}
	if _, err := store.ReadRaw(caseFile.ID, "raw_loser"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("losing bundle leaked raw output: %v", err)
	}

	if runtime.GOOS != "windows" {
		taskDir, dirErr := store.TaskDir(caseFile.ID)
		if dirErr != nil {
			t.Fatal(dirErr)
		}
		for _, path := range []string{
			filepath.Join(taskDir, "case.json"),
			filepath.Join(taskDir, "evidence.jsonl"),
			filepath.Join(taskDir, "verification.json"),
			filepath.Join(taskDir, "raw", "raw_bundle.txt"),
		} {
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("%s permissions = %o, want 600", path, info.Mode().Perm())
			}
		}
	}
}

func TestCommitVerificationBundleRejectsOversizedEvidenceBeforePublishing(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.CaseFile{ID: "task_bundle_limit", Status: domain.PhaseChanging}
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	beforeRevision := c.Revision
	c.Status = domain.PhaseVerifying
	evidence := domain.Evidence{
		ID: "ev_huge_bundle", Timestamp: time.Now().UTC(), Kind: domain.KindCodeGraph,
		Source: domain.Source{Tool: "codemap"}, Claim: strings.Repeat("x", maxLedgerRecordBytes+1),
		Confidence: domain.ConfidenceHigh,
	}
	receipt := domain.VerificationRecord{
		ID: "vr_huge_bundle", BatchID: "vb_huge_bundle", Claim: "review",
		Status: domain.VerifyPassed, Purpose: domain.VerificationPurposeVerifierRun,
		Evidence: []string{evidence.ID}, Timestamp: time.Now().UTC(),
	}
	err = store.CommitVerificationBundle(c, []domain.Evidence{evidence}, []domain.VerificationRecord{receipt}, nil)
	if err == nil || !strings.Contains(err.Error(), "record exceeds") {
		t.Fatalf("oversized bundle error = %v", err)
	}
	durable, loadErr := store.Load(c.ID)
	if loadErr != nil || durable.Revision != beforeRevision || durable.Status != domain.PhaseChanging {
		t.Fatalf("rejected bundle changed case: %+v err=%v", durable, loadErr)
	}
	items, _ := store.Evidence(c.ID)
	receipts, _ := store.Verifications(c.ID)
	if len(items) != 0 || len(receipts) != 0 {
		t.Fatalf("rejected bundle leaked evidence/receipts: %+v %+v", items, receipts)
	}
}

func TestVerificationReadersRecoverInterruptedBundleBeforeExposure(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.CaseFile{ID: "task_bundle_recovery", Status: domain.PhaseChanging}
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	nextCase := *c
	nextCase.Status = domain.PhaseVerifying
	next := nextCaseSnapshot(&nextCase, c.Revision)
	evidence := domain.Evidence{
		ID: "ev_interrupted", Timestamp: time.Now().UTC(), Kind: domain.KindCodeGraph,
		Source: domain.Source{Tool: "codemap"}, Claim: "must remain hidden", Confidence: domain.ConfidenceHigh,
		RawRef: "case://task_bundle_recovery/raw/raw_interrupted",
	}
	ledger, err := store.evidenceLedgerWithManyUnlocked(c.ID, []domain.Evidence{evidence})
	if err != nil {
		t.Fatal(err)
	}
	receipts := []domain.VerificationRecord{{
		ID: "vr_interrupted", BatchID: "vb_interrupted", Claim: "must remain hidden",
		Status: domain.VerifyPassed, Purpose: domain.VerificationPurposeVerifierRun,
		Evidence: []string{evidence.ID}, Timestamp: time.Now().UTC(),
	}}
	files, err := store.stageTransactionFiles(c.ID, next.Revision, []transactionValue{
		{name: filepath.Join("raw", "raw_interrupted.txt"), bytes: []byte("hidden raw")},
		{name: "evidence.jsonl", bytes: ledger},
		{name: "verification.json", value: receipts},
		{name: "case.json", value: &next},
	})
	if err != nil {
		t.Fatal(err)
	}
	journal := transactionJournal{Revision: next.Revision}
	for _, file := range files {
		name, _ := filepath.Rel(store.dir(c.ID), file.target)
		stage, _ := filepath.Rel(store.dir(c.ID), file.stage)
		journal.Files = append(journal.Files, transactionJournalFile{
			Name: filepath.ToSlash(name), Stage: filepath.ToSlash(stage),
			Old: append([]byte(nil), file.old...), Existed: file.existed,
		})
	}
	if err := writeJSON(filepath.Join(store.dir(c.ID), transactionJournalName), journal); err != nil {
		t.Fatal(err)
	}
	// Simulate death after raw/evidence/receipts were renamed but before the
	// case.json commit anchor.
	for _, file := range files[:3] {
		if err := os.Rename(file.stage, file.target); err != nil {
			t.Fatal(err)
		}
	}

	gotReceipts, err := store.Verifications(c.ID)
	if err != nil || len(gotReceipts) != 0 {
		t.Fatalf("reader exposed interrupted receipts: %+v err=%v", gotReceipts, err)
	}
	if _, err := store.ReadRaw(c.ID, "raw_interrupted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reader exposed interrupted raw output: %v", err)
	}
	gotEvidence, err := store.Evidence(c.ID)
	if err != nil || len(gotEvidence) != 0 {
		t.Fatalf("reader exposed interrupted evidence: %+v err=%v", gotEvidence, err)
	}
	durable, err := store.Load(c.ID)
	if err != nil || durable.Status != domain.PhaseChanging || durable.Revision != c.Revision {
		t.Fatalf("interrupted case anchor leaked: %+v err=%v", durable, err)
	}
}
