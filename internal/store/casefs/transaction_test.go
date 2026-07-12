package casefs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestCommitPlanAcrossStoresKeepsCompanionSnapshotsConsistent(t *testing.T) {
	root := t.TempDir()
	s1, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	if err := s1.Create(c); err != nil {
		t.Fatal(err)
	}

	a, err := s1.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	planA, hypothesesA := transactionPlan("a")
	planB, hypothesesB := transactionPlan("b")
	preparePlannedCase(a, planA)
	preparePlannedCase(b, planB)

	type result struct {
		label string
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, attempt := range []struct {
		label      string
		store      *Store
		caseFile   *domain.CaseFile
		plan       domain.Plan
		hypotheses []domain.Hypothesis
	}{
		{label: "a", store: s1, caseFile: a, plan: planA, hypotheses: hypothesesA},
		{label: "b", store: s2, caseFile: b, plan: planB, hypotheses: hypothesesB},
	} {
		wg.Add(1)
		go func(attempt struct {
			label      string
			store      *Store
			caseFile   *domain.CaseFile
			plan       domain.Plan
			hypotheses []domain.Hypothesis
		}) {
			defer wg.Done()
			<-start
			results <- result{label: attempt.label, err: attempt.store.CommitPlan(attempt.caseFile, attempt.plan, attempt.hypotheses)}
		}(attempt)
	}
	close(start)
	wg.Wait()
	close(results)

	winner := ""
	conflicts := 0
	for got := range results {
		switch {
		case got.err == nil:
			winner = got.label
		case errors.Is(got.err, ErrRevisionConflict):
			conflicts++
			var conflict *RevisionConflictError
			if !errors.As(got.err, &conflict) || !conflict.Retryable() || conflict.Expected != 1 || conflict.Actual != 2 {
				t.Fatalf("unexpected conflict: %#v", got.err)
			}
		default:
			t.Fatalf("unexpected commit error: %v", got.err)
		}
	}
	if winner == "" || conflicts != 1 {
		t.Fatalf("winner=%q conflicts=%d, want one of each", winner, conflicts)
	}

	finalCase, err := s1.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	finalPlan, err := s1.LoadPlan(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	finalHypotheses, err := s1.Hypotheses(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantPlan, wantHypotheses := planA, hypothesesA
	if winner == "b" {
		wantPlan, wantHypotheses = planB, hypothesesB
	}
	if finalCase.Revision != 2 || finalCase.Status != domain.PhasePlanned {
		t.Fatalf("final case = %+v", finalCase)
	}
	if !reflect.DeepEqual(finalCase.ChangeBoundary, wantPlan.ChangeBoundary) ||
		!reflect.DeepEqual(finalCase.VerificationRequired, wantPlan.VerificationRequired) ||
		!reflect.DeepEqual(finalPlan, wantPlan) ||
		!reflect.DeepEqual(finalHypotheses, wantHypotheses) {
		t.Fatalf("mixed transaction snapshots: case=%+v plan=%+v hypotheses=%+v winner=%s", finalCase, finalPlan, finalHypotheses, winner)
	}
}

func TestUpdateHypothesesAcrossStoresReturnsTypedConflictAndPreservesRetry(t *testing.T) {
	root := t.TempDir()
	s1, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	if err := s1.Create(c); err != nil {
		t.Fatal(err)
	}
	hypotheses := []domain.Hypothesis{
		transactionHypothesis("one"),
		transactionHypothesis("two"),
	}
	plan := domain.Plan{
		Hypotheses:     hypotheses,
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"shared.go"}},
		Uncertainty:    "both explanations remain uncertain",
	}
	preparePlannedCase(c, plan)
	if err := s1.CommitPlan(c, plan, hypotheses); err != nil {
		t.Fatal(err)
	}
	a, err := s1.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}

	type attempt struct {
		store    *Store
		caseFile *domain.CaseFile
		target   string
		status   domain.HypothesisStatus
		evidence string
	}
	type result struct {
		attempt attempt
		err     error
	}
	attempts := []attempt{
		{store: s1, caseFile: a, target: "hyp_one", status: domain.HypRejected, evidence: "ev_one"},
		{store: s2, caseFile: b, target: "hyp_two", status: domain.HypChallenged, evidence: "ev_two"},
	}
	start := make(chan struct{})
	results := make(chan result, len(attempts))
	var wg sync.WaitGroup
	for _, current := range attempts {
		wg.Add(1)
		go func(current attempt) {
			defer wg.Done()
			<-start
			_, _, err := current.store.UpdateHypotheses(current.caseFile, hypothesisResolution(current.target, current.status, current.evidence))
			results <- result{attempt: current, err: err}
		}(current)
	}
	close(start)
	wg.Wait()
	close(results)

	var loser attempt
	successes := 0
	conflicts := 0
	for got := range results {
		switch {
		case got.err == nil:
			successes++
		case errors.Is(got.err, ErrRevisionConflict):
			conflicts++
			loser = got.attempt
			var conflict *RevisionConflictError
			if !errors.As(got.err, &conflict) || !conflict.Retryable() || conflict.Expected != 2 || conflict.Actual != 3 {
				t.Fatalf("unexpected conflict: %#v", got.err)
			}
		default:
			t.Fatalf("unexpected update error: %v", got.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want one each", successes, conflicts)
	}

	latest, err := loser.store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := loser.store.UpdateHypotheses(latest, hypothesisResolution(loser.target, loser.status, loser.evidence)); err != nil {
		t.Fatalf("retry latest snapshot: %v", err)
	}
	finalCase, err := s1.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	finalHypotheses, err := s1.Hypotheses(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalCase.Revision != 4 {
		t.Fatalf("final revision = %d, want 4", finalCase.Revision)
	}
	statuses := make(map[string]domain.HypothesisStatus, len(finalHypotheses))
	for _, hypothesis := range finalHypotheses {
		statuses[hypothesis.ID] = hypothesis.Status
	}
	if statuses["hyp_one"] != domain.HypRejected || statuses["hyp_two"] != domain.HypChallenged {
		t.Fatalf("distinct resolutions were not preserved: %+v", finalHypotheses)
	}
	evidence, err := s1.Evidence(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 || evidence[0].ID == evidence[1].ID {
		t.Fatalf("resolution evidence = %+v, want two unique records", evidence)
	}
}

func TestInterruptedPlanTransactionRollsBackBeforeReadersObserveIt(t *testing.T) {
	store, c, oldPlan, oldHypotheses := committedTransactionFixture(t)
	newPlan, newHypotheses := transactionPlan("new")
	files := stageInterruptedPlan(t, store, c, newPlan, newHypotheses)
	// Simulate a process dying after publishing plan + hypotheses but before the
	// final case.json commit anchor.
	for _, file := range files[:2] {
		if err := os.Rename(file.stage, file.target); err != nil {
			t.Fatal(err)
		}
	}

	recoveredCase, err := store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredPlan, err := store.LoadPlan(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredHypotheses, err := store.Hypotheses(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredCase.Revision != c.Revision || !reflect.DeepEqual(recoveredPlan, oldPlan) || !reflect.DeepEqual(recoveredHypotheses, oldHypotheses) {
		t.Fatalf("interrupted transaction leaked mixed snapshots: case=%+v plan=%+v hypotheses=%+v", recoveredCase, recoveredPlan, recoveredHypotheses)
	}
	if _, err := os.Stat(filepath.Join(store.dir(c.ID), transactionJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered journal still exists: %v", err)
	}
}

func TestCommittedPlanTransactionWithLeftoverJournalRollsForwardCleanup(t *testing.T) {
	store, c, _, _ := committedTransactionFixture(t)
	newPlan, newHypotheses := transactionPlan("new")
	files := stageInterruptedPlan(t, store, c, newPlan, newHypotheses)
	// Simulate a process dying after the final case anchor landed but before it
	// removed the durable journal.
	for _, file := range files {
		if err := os.Rename(file.stage, file.target); err != nil {
			t.Fatal(err)
		}
	}

	recoveredCase, err := store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredPlan, err := store.LoadPlan(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredHypotheses, err := store.Hypotheses(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredCase.Revision != c.Revision+1 || !reflect.DeepEqual(recoveredPlan, newPlan) || !reflect.DeepEqual(recoveredHypotheses, newHypotheses) {
		t.Fatalf("committed transaction did not survive journal cleanup: case=%+v plan=%+v hypotheses=%+v", recoveredCase, recoveredPlan, recoveredHypotheses)
	}
	if _, err := os.Stat(filepath.Join(store.dir(c.ID), transactionJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed journal still exists: %v", err)
	}
}

func committedTransactionFixture(t *testing.T) (*Store, *domain.CaseFile, domain.Plan, []domain.Hypothesis) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	plan, hypotheses := transactionPlan("old")
	preparePlannedCase(c, plan)
	if err := store.CommitPlan(c, plan, hypotheses); err != nil {
		t.Fatal(err)
	}
	return store, c, plan, hypotheses
}

func stageInterruptedPlan(t *testing.T, store *Store, current *domain.CaseFile, plan domain.Plan, hypotheses []domain.Hypothesis) []stagedFile {
	t.Helper()
	c := *current
	preparePlannedCase(&c, plan)
	next := nextCaseSnapshot(&c, c.Revision)
	files, err := store.stageTransactionFiles(c.ID, next.Revision, []transactionValue{
		{name: "plan.json", value: plan},
		{name: "hypotheses.json", value: hypotheses},
		{name: "case.json", value: &next},
	})
	if err != nil {
		t.Fatal(err)
	}
	journal := transactionJournal{Revision: next.Revision, Files: make([]transactionJournalFile, 0, len(files))}
	for _, file := range files {
		journal.Files = append(journal.Files, transactionJournalFile{
			Name: filepath.Base(file.target), Stage: filepath.Base(file.stage),
			Old: append([]byte(nil), file.old...), Existed: file.existed,
		})
	}
	if err := writeJSON(filepath.Join(store.dir(c.ID), transactionJournalName), journal); err != nil {
		t.Fatal(err)
	}
	return files
}

func transactionPlan(label string) (domain.Plan, []domain.Hypothesis) {
	hypotheses := []domain.Hypothesis{transactionHypothesis(label)}
	return domain.Plan{
		Hypotheses:           hypotheses,
		ChangeBoundary:       domain.ChangeBoundary{Files: []string{label + ".go"}},
		VerificationRequired: []string{"codemap_review"},
		Uncertainty:          "plan " + label + " remains uncertain",
	}, hypotheses
}

func transactionHypothesis(label string) domain.Hypothesis {
	return domain.Hypothesis{
		ID:         "hyp_" + label,
		Statement:  "hypothesis " + label,
		Confidence: domain.ConfidenceLow,
		DisproveBy: domain.Disproof{Note: "disprove " + label},
		Status:     domain.HypActive,
	}
}

func preparePlannedCase(c *domain.CaseFile, plan domain.Plan) {
	c.Status = domain.PhasePlanned
	c.ChangeBoundary = plan.ChangeBoundary
	c.VerificationRequired = plan.VerificationRequired
}

func hypothesisResolution(target string, status domain.HypothesisStatus, evidenceID string) HypothesesUpdate {
	return func(current []domain.Hypothesis) ([]domain.Hypothesis, *domain.Evidence, error) {
		for i := range current {
			if current[i].ID == target {
				current[i].Status = status
				return current, &domain.Evidence{
					ID:          evidenceID,
					Timestamp:   time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
					Kind:        domain.KindHumanReport,
					Source:      domain.Source{Tool: "human"},
					Claim:       "resolved " + target,
					Confidence:  domain.ConfidenceMedium,
					Sensitivity: domain.SensitivityNormal,
				}, nil
			}
		}
		return nil, nil, errors.New("hypothesis not found")
	}
}
