package casefs

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestCaseRevisionCreateLoadAndSave(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	c.Actor = "agent-a"
	c.ParentTaskID = "task_parent"
	c.ChildTaskIDs = []string{"task_child"}
	c.IdempotencyKey = "open-123"
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	if c.Revision != 1 {
		t.Fatalf("created revision = %d, want 1", c.Revision)
	}

	c.Notes = append(c.Notes, "updated")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if c.Revision != 2 {
		t.Fatalf("saved revision = %d, want 2", c.Revision)
	}
	got, err := s.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || got.Actor != "agent-a" || got.ParentTaskID != "task_parent" ||
		len(got.ChildTaskIDs) != 1 || got.IdempotencyKey != "open-123" {
		t.Fatalf("snapshot metadata did not round-trip: %+v", got)
	}
}

func TestSaveCompareAndSwapAcrossStores(t *testing.T) {
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
	a, _ := s1.Load(c.ID)
	b, _ := s2.Load(c.ID)
	a.Notes = []string{"won by a"}
	b.Notes = []string{"won by b"}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, attempt := range []struct {
		store    *Store
		caseFile *domain.CaseFile
	}{{s1, a}, {s2, b}} {
		wg.Add(1)
		go func(store *Store, caseFile *domain.CaseFile) {
			defer wg.Done()
			<-start
			errs <- store.Save(caseFile)
		}(attempt.store, attempt.caseFile)
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRevisionConflict):
			conflicts++
			var conflict *RevisionConflictError
			if !errors.As(err, &conflict) || !conflict.Retryable() || conflict.Expected != 1 || conflict.Actual != 2 {
				t.Fatalf("unexpected conflict details: %#v", err)
			}
		default:
			t.Fatalf("unexpected save error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want one each", successes, conflicts)
	}
	got, err := s1.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || len(got.Notes) != 1 || (got.Notes[0] != "won by a" && got.Notes[0] != "won by b") {
		t.Fatalf("winner snapshot not preserved: %+v", got)
	}
}

func TestLegacyCaseRevisionUpgradesOnSave(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	legacy := *c
	legacy.Revision = 0
	if err := writeJSON(filepath.Join(s.dir(c.ID), "case.json"), &legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 {
		t.Fatalf("legacy load revision = %d, want implicit 1", loaded.Revision)
	}
	if err := s.Save(loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 2 {
		t.Fatalf("materialized revision = %d, want 2", loaded.Revision)
	}
}
