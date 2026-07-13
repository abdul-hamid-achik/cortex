package casefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestAppendVerificationAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	s1, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.CaseFile{ID: "task_concurrent", Goal: "g", Status: domain.PhaseInvestigating}
	if err := s1.Create(c); err != nil {
		t.Fatal(err)
	}
	const perStore = 25
	var wg sync.WaitGroup
	for i := 0; i < perStore*2; i++ {
		store := s1
		if i%2 == 1 {
			store = s2
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.AppendVerification(c.ID, domain.VerificationRecord{
				ID: fmt.Sprintf("vr_%d", i), Claim: fmt.Sprintf("claim %d", i),
				Surface: domain.SurfaceCode, Status: domain.VerifyPassed, Timestamp: time.Now().UTC(),
			}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	recs, err := s1.Verifications(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != perStore*2 {
		t.Fatalf("lost concurrent receipts: got %d want %d", len(recs), perStore*2)
	}
}

func TestAppendLedgerRecordRejectsUnboundedPayload(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	err := s.AppendEvidence(c.ID, domain.Evidence{
		ID: "ev_huge", Timestamp: time.Now().UTC(), Kind: domain.KindHumanReport,
		Source: domain.Source{Origin: "human"}, Claim: strings.Repeat("x", maxLedgerRecordBytes+1),
		Confidence: domain.ConfidenceMedium,
	})
	if err == nil || !strings.Contains(err.Error(), "record exceeds") {
		t.Fatalf("oversized ledger record error = %v", err)
	}
	items, readErr := s.Evidence(c.ID)
	if readErr != nil || len(items) != 0 {
		t.Fatalf("rejected ledger record was written: items=%d err=%v", len(items), readErr)
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "cases"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

func sampleCase() *domain.CaseFile {
	return &domain.CaseFile{
		ID:        "task_TEST01",
		CreatedAt: time.Now().UTC(),
		Goal:      "fix redirect",
		Mode:      domain.ModeChange,
		Status:    domain.PhaseInvestigating,
		Workspace: domain.Workspace{Root: "/tmp/x", Repository: "x", Branch: "main"},
		Surfaces:  []domain.Surface{domain.SurfaceCode},
	}
}

func TestPhaseEventsRoundTrip(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = s.AppendPhaseEvent(c.ID, PhaseEvent{Timestamp: time.Now().UTC(), From: domain.PhaseNew, To: domain.PhaseOrienting})
	_ = s.AppendPhaseEvent(c.ID, PhaseEvent{Timestamp: time.Now().UTC(), From: domain.PhaseOrienting, To: domain.PhaseInvestigating})
	evs, err := s.PhaseEvents(c.ID)
	if err != nil {
		t.Fatalf("phase events: %v", err)
	}
	if len(evs) != 2 || evs[0].To != domain.PhaseOrienting || evs[1].To != domain.PhaseInvestigating {
		t.Errorf("unexpected phase history: %+v", evs)
	}
	// A case with no history returns nil, not an error.
	if got, err := s.PhaseEvents("task_absent"); err != nil || got != nil {
		t.Errorf("absent phases should be (nil,nil), got (%v,%v)", got, err)
	}
}

func TestCaseRoundTrip(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	c.AcceptanceCriteria = []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "tests pass"}}
	if err := s.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Load(c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Goal != c.Goal || got.Status != c.Status || got.Workspace.Branch != "main" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.SchemaVersion != domain.SchemaVersion {
		t.Errorf("schema version not stamped: %d", got.SchemaVersion)
	}
	if len(got.AcceptanceCriteria) != 1 || got.AcceptanceCriteria[0] != c.AcceptanceCriteria[0] {
		t.Errorf("acceptance criteria round-trip mismatch: %+v", got.AcceptanceCriteria)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(c); err == nil {
		t.Error("creating an existing case should error")
	}
}

func TestAcceptanceCriteriaAreImmutableAfterCreate(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	c.AcceptanceCriteria = []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "tests pass"}}
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	c.AcceptanceCriteria[0].Statement = "different"
	if err := s.Save(c); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("mutated acceptance criteria save error = %v", err)
	}
	loaded, err := s.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AcceptanceCriteria[0].Statement != "tests pass" {
		t.Fatalf("immutable criteria changed on disk: %+v", loaded.AcceptanceCriteria)
	}
}

func TestAcceptanceCriteriaAreImmutableAcrossTransactionalWrites(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	c.AcceptanceCriteria = []domain.AcceptanceCriterion{{ID: "criterion_1", Statement: "tests pass"}}
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	c.AcceptanceCriteria[0].Statement = "different"
	c.Status = domain.PhasePlanned
	plan := domain.Plan{
		Hypotheses: []domain.Hypothesis{{
			ID: "hyp_1", Statement: "h", DisproveBy: domain.Disproof{Note: "d"}, Status: domain.HypActive,
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"a.go"}}, Uncertainty: "u",
	}
	c.ChangeBoundary = plan.ChangeBoundary
	if err := s.CommitPlan(c, plan, plan.Hypotheses); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("transactional criteria mutation error = %v", err)
	}
}

func TestLoadRejectsInvalidAcceptanceCriteria(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	c.AcceptanceCriteria = []domain.AcceptanceCriterion{
		{ID: "same", Statement: "first"},
		{ID: "same", Statement: "second"},
	}
	if err := writeJSON(filepath.Join(s.dir(c.ID), "case.json"), c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(c.ID); err == nil || !strings.Contains(err.Error(), "duplicate acceptance criterion") {
		t.Fatalf("invalid acceptance criteria load error = %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	s := newStore(t)
	if _, err := s.Load("task_NOPE"); err == nil {
		t.Error("loading a missing case should error")
	}
}

func TestEvidenceAppendAndGet(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	ev := domain.Evidence{ID: "ev_1", Timestamp: time.Now(), Kind: domain.KindCodeGraph, Source: domain.Source{Tool: "codemap"}, Claim: "x calls y", Confidence: domain.ConfidenceHigh}
	if err := s.AppendEvidence(c.ID, ev); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.AppendEvidence(c.ID, domain.Evidence{ID: "ev_2", Timestamp: time.Now(), Source: domain.Source{Tool: "git"}, Claim: "changed file"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.Evidence(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 evidence records, got %d", len(all))
	}
	got, err := s.GetEvidence(c.ID, "ev_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Claim != "x calls y" {
		t.Errorf("wrong evidence: %+v", got)
	}
}

func TestAppendEvidenceRejectsInvalid(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	// No claim → invalid.
	if err := s.AppendEvidence(c.ID, domain.Evidence{ID: "ev_x", Timestamp: time.Now(), Source: domain.Source{Tool: "x"}}); err == nil {
		t.Error("invalid evidence should be rejected")
	}
}

func TestPlanAndVerificationRoundTrip(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	plan := domain.Plan{
		Hypotheses:     []domain.Hypothesis{{ID: "hyp_1", Statement: "h", DisproveBy: domain.Disproof{Note: "d"}, Status: domain.HypActive}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"a.go"}},
		Uncertainty:    "unsure",
	}
	if err := s.SavePlan(c.ID, plan); err != nil {
		t.Fatal(err)
	}
	gotPlan, err := s.LoadPlan(c.ID)
	if err != nil || len(gotPlan.Hypotheses) != 1 {
		t.Fatalf("plan round-trip failed: %v %+v", err, gotPlan)
	}

	vr := domain.VerificationRecord{ID: "vr_1", Claim: "c", Surface: domain.SurfaceCode, Status: domain.VerifyPassed, Timestamp: time.Now()}
	if err := s.AppendVerification(c.ID, vr); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendVerification(c.ID, domain.VerificationRecord{ID: "vr_2", Claim: "c2", Status: domain.VerifyNotRun, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	recs, err := s.Verifications(c.ID)
	if err != nil || len(recs) != 2 {
		t.Fatalf("expected 2 verification records, got %d (%v)", len(recs), err)
	}
}

func TestRawRoundTrip(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	if err := s.WriteRaw(c.ID, "raw_ABC", "the raw tool output"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRaw(c.ID, "raw_ABC")
	if err != nil {
		t.Fatal(err)
	}
	if got != "the raw tool output" {
		t.Errorf("raw round-trip mismatch: %q", got)
	}
	if _, err := s.ReadRaw(c.ID, "raw_MISSING"); err == nil {
		t.Error("missing raw should error")
	}
}

func TestRawIDIsSanitized(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	// A traversal-style id must not escape the raw/ dir.
	if err := s.WriteRaw(c.ID, "../../etc/passwd", "x"); err != nil {
		t.Fatal(err)
	}
	// It is readable back through the same sanitization, and nothing was written
	// outside the case dir.
	if _, err := s.ReadRaw(c.ID, "../../etc/passwd"); err != nil {
		t.Errorf("sanitized id should round-trip: %v", err)
	}
}

func TestTaskIDRejectsTraversalAndSanitizationAliases(t *testing.T) {
	// Regression: cleaning an invalid ID is not sufficient. task/alias used to
	// resolve to the same directory as task_alias, crossing task ownership.
	root := filepath.Join(t.TempDir(), "cases")
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleCase()
	actual.ID = "task_alias"
	if err := s.Create(actual); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"../../../etc", "..", "a/../../b", "/absolute/evil",
		"task/alias", "task.alias", "task_alias/..", "coord_not_a_task",
	} {
		if _, err := s.Load(invalid); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("Load(%q) error = %v, want ErrInvalidTaskID", invalid, err)
		}
		if _, err := s.TaskDir(invalid); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("TaskDir(%q) error = %v, want ErrInvalidTaskID", invalid, err)
		}
		if err := s.RemoveTask(invalid); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("RemoveTask(%q) error = %v, want ErrInvalidTaskID", invalid, err)
		}
	}
	if _, err := s.Load(actual.ID); err != nil {
		t.Fatalf("invalid alias affected canonical task: %v", err)
	}
	if got, err := s.TaskDir("task_06FKABC123"); err != nil || got != filepath.Join(root, "task_06FKABC123") {
		t.Errorf("legit id should be unchanged, got %q err=%v", got, err)
	}
}

func TestList(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"task_A", "task_B", "task_C"} {
		c := sampleCase()
		c.ID = id
		_ = s.Create(c)
	}
	ids, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(ids))
	}
	// Newest first (reverse lexical): C, B, A.
	if ids[0] != "task_C" {
		t.Errorf("expected newest first, got %v", ids)
	}
}

func TestCreateRejectsEmptyID(t *testing.T) {
	s := newStore(t)
	if err := s.Create(&domain.CaseFile{}); err == nil {
		t.Error("creating a case with no id should error")
	}
}

func TestLoadCorruptCaseErrors(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	// Corrupt case.json → Load must surface a parse error, not silently succeed.
	if err := os.WriteFile(filepath.Join(s.dir(c.ID), "case.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(c.ID); err == nil {
		t.Error("loading a corrupt case.json should error")
	}
}

func TestEvidenceCorruptLineErrors(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	if err := os.WriteFile(filepath.Join(s.dir(c.ID), "evidence.jsonl"), []byte("{bad json line}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evidence(c.ID); err == nil {
		t.Error("a corrupt evidence line should error")
	}
}

func TestVerificationsCorruptErrors(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	if err := os.WriteFile(filepath.Join(s.dir(c.ID), "verification.json"), []byte("nonsense"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verifications(c.ID); err == nil {
		t.Error("a corrupt verification.json should error")
	}
	// AppendVerification must fail too (it read-modify-writes).
	if err := s.AppendVerification(c.ID, domain.VerificationRecord{Claim: "c", Status: domain.VerifyPassed, Timestamp: time.Now()}); err == nil {
		t.Error("appending onto a corrupt verification.json should error")
	}
}

func TestSaveToUnwritableDirErrors(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	_ = s.Create(c)
	dir := s.dir(c.ID)
	if err := os.Chmod(dir, 0o500); err != nil { // read+execute only
		t.Skip("cannot chmod in this environment")
	}
	defer func() { _ = os.Chmod(dir, 0o755) }() // restore so TempDir cleanup works
	if err := s.Save(c); err == nil {
		t.Error("saving into a read-only case dir should error")
	}
}
