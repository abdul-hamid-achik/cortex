package casefs

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func storeDecision(id string) domain.Decision {
	return domain.Decision{
		ID: id, Question: "Choose a repair", Requester: "agent",
		RequestedAt: time.Now().UTC(), Status: domain.DecisionPending,
		Options: []domain.DecisionOption{
			{ID: "small", Label: "Small", Consequence: "Low risk"},
			{ID: "broad", Label: "Broad", Consequence: "More change"},
		},
	}
}

func TestDecisionRoundTrip(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	d := storeDecision("dec_1")
	if err := s.AppendDecision(c.ID, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Decision(c.ID, d.ID)
	if err != nil || got.Status != domain.DecisionPending {
		t.Fatalf("pending decision round-trip: %+v (%v)", got, err)
	}
	answered, err := s.AnswerDecision(c.ID, d.ID, "small", "human", "ev_1", time.Now().UTC(), false)
	if err != nil {
		t.Fatal(err)
	}
	if answered.Status != domain.DecisionAnswered || answered.Answer != "small" || answered.Responder != "human" {
		t.Fatalf("answer not persisted: %+v", answered)
	}
	if _, err := s.AnswerDecision(c.ID, d.ID, "broad", "other", "ev_2", time.Now().UTC(), false); err == nil {
		t.Error("answering an answered decision must fail")
	}
	if err := s.AppendDecision(c.ID, storeDecision("dec_2")); err != nil {
		t.Fatalf("a new request after the prior answer should succeed: %v", err)
	}
	if got, err := s.Decisions(c.ID); err != nil || len(got) != 2 {
		t.Fatalf("decision history: %d records (%v)", len(got), err)
	}
}

func TestAnswerDecisionAcrossStoreInstancesIsSerialized(t *testing.T) {
	root := t.TempDir()
	s1, _ := New(root)
	s2, _ := New(root)
	c := sampleCase()
	if err := s1.Create(c); err != nil {
		t.Fatal(err)
	}
	if err := s1.AppendDecision(c.ID, storeDecision("dec_race")); err != nil {
		t.Fatal(err)
	}

	var successes atomic.Int32
	var wg sync.WaitGroup
	for i, store := range []*Store{s1, s2} {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			answer := "small"
			if i == 1 {
				answer = "broad"
			}
			if _, err := store.AnswerDecision(c.ID, "dec_race", answer, "human", "ev_race", time.Now().UTC(), false); err == nil {
				successes.Add(1)
			}
		}(i, store)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("exactly one racing answer should win, got %d", successes.Load())
	}
	got, err := s1.Decision(c.ID, "dec_race")
	if err != nil || got.Status != domain.DecisionAnswered {
		t.Fatalf("winning answer missing: %+v (%v)", got, err)
	}
}

func TestDecisionReadModifyWriteDoesNotLoseCrossInstanceUpdates(t *testing.T) {
	root := t.TempDir()
	s1, _ := New(root)
	s2, _ := New(root)
	c := sampleCase()
	if err := s1.Create(c); err != nil {
		t.Fatal(err)
	}
	// Seed two pending records directly: the kernel normally permits one pending
	// decision at a time, but this fixture lets two stores update different array
	// elements concurrently and proves the whole-file RMW is serialized.
	seed := []domain.Decision{storeDecision("dec_a"), storeDecision("dec_b")}
	if err := writeJSON(filepath.Join(s1.dir(c.ID), "decisions.json"), seed); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i, tc := range []struct {
		store *Store
		id    string
	}{
		{s1, "dec_a"}, {s2, "dec_b"},
	} {
		wg.Add(1)
		go func(i int, tc struct {
			store *Store
			id    string
		}) {
			defer wg.Done()
			if _, err := tc.store.AnswerDecision(c.ID, tc.id, "small", "human", "ev_"+tc.id, time.Now().UTC(), false); err != nil {
				t.Errorf("answer %d: %v", i, err)
			}
		}(i, tc)
	}
	wg.Wait()
	decisions, err := s1.Decisions(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if decision.Status != domain.DecisionAnswered {
			t.Fatalf("lost cross-instance update: %+v", decisions)
		}
	}
}

func TestAppendEvidenceOnceAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	s1, _ := New(root)
	s2, _ := New(root)
	c := sampleCase()
	if err := s1.Create(c); err != nil {
		t.Fatal(err)
	}
	ev := domain.Evidence{
		ID: "ev_once", Timestamp: time.Now().UTC(), Kind: domain.KindHumanReport,
		Source: domain.Source{Origin: "human"}, Claim: "one durable decision", Confidence: domain.ConfidenceMedium,
	}
	var inserted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		store := s1
		if i%2 == 1 {
			store = s2
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, added, err := store.AppendEvidenceOnce(c.ID, ev)
			if err != nil {
				t.Errorf("append once: %v", err)
				return
			}
			if got.ID != ev.ID {
				t.Errorf("durable evidence = %+v", got)
			}
			if added {
				inserted.Add(1)
			}
		}()
	}
	wg.Wait()
	if inserted.Load() != 1 {
		t.Fatalf("evidence inserted %d times, want 1", inserted.Load())
	}
	all, err := s1.Evidence(c.ID)
	if err != nil || len(all) != 1 || all[0].ID != ev.ID {
		t.Fatalf("evidence ledger = %+v (%v)", all, err)
	}
}
