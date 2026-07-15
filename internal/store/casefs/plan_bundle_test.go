package casefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

type cancelAfterErrChecks struct {
	context.Context
	checks int
	done   chan struct{}
}

func (c *cancelAfterErrChecks) Err() error {
	c.checks++
	if c.checks >= 3 {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		return context.Canceled
	}
	return nil
}

func (c *cancelAfterErrChecks) Done() <-chan struct{} { return c.done }

func TestCommitPlanBundlePublishesAllCompanionsAtomically(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	c.ID = "task_plan_bundle"
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	plan, hypotheses := transactionPlan("bundle")
	preparePlannedCase(c, plan)
	evidence := []domain.Evidence{planBundleEvidence(c.ID, "ev_bob_bundle", "raw_bob_bundle", "Bob classifies bundle.go as managed")}
	raws := []RawRecord{{ID: "raw_bob_bundle", Content: "bounded redacted Bob path output"}}

	if err := store.CommitPlanBundle(c, plan, hypotheses, evidence, raws); err != nil {
		t.Fatal(err)
	}
	durableCase, err := store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	durablePlan, err := store.LoadPlan(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	durableHypotheses, err := store.Hypotheses(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	durableEvidence, err := store.Evidence(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	durableRaw, err := store.ReadRaw(c.ID, raws[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if durableCase.Revision != 2 || durableCase.Status != domain.PhasePlanned ||
		!reflect.DeepEqual(durablePlan, plan) || !reflect.DeepEqual(durableHypotheses, hypotheses) ||
		!reflect.DeepEqual(durableEvidence, evidence) || durableRaw != raws[0].Content {
		t.Fatalf("bundle did not publish coherently: case=%+v plan=%+v hypotheses=%+v evidence=%+v raw=%q", durableCase, durablePlan, durableHypotheses, durableEvidence, durableRaw)
	}
}

func TestCommitPlanBundleExactRetryIsIdempotent(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	c.ID = "task_plan_bundle_retry"
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	plan, hypotheses := transactionPlan("retry")
	preparePlannedCase(c, plan)
	stale := *c
	evidence := []domain.Evidence{planBundleEvidence(c.ID, "ev_bob_retry", "raw_bob_retry", "Bob reports retry repository state clean")}
	raws := []RawRecord{{ID: "raw_bob_retry", Content: "stable Bob context"}}

	if err := store.CommitPlanBundle(c, plan, hypotheses, evidence, raws); err != nil {
		t.Fatal(err)
	}
	committedRevision := c.Revision
	if err := store.CommitPlanBundle(&stale, plan, hypotheses, evidence, raws); err != nil {
		t.Fatalf("stale exact retry: %v", err)
	}
	if stale.Revision != committedRevision {
		t.Fatalf("stale retry adopted revision %d, want %d", stale.Revision, committedRevision)
	}
	if err := store.CommitPlanBundle(c, plan, hypotheses, evidence, raws); err != nil {
		t.Fatalf("current exact retry: %v", err)
	}
	if c.Revision != committedRevision {
		t.Fatalf("exact retry advanced revision to %d, want %d", c.Revision, committedRevision)
	}
	durableEvidence, err := store.Evidence(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durableEvidence) != 1 {
		t.Fatalf("exact retry duplicated evidence: %+v", durableEvidence)
	}

	changedRaw := []RawRecord{{ID: raws[0].ID, Content: "different Bob context"}}
	if err := store.CommitPlanBundle(c, plan, hypotheses, evidence, changedRaw); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("changed raw retry error = %v", err)
	}
	durableRaw, err := store.ReadRaw(c.ID, raws[0].ID)
	if err != nil || durableRaw != raws[0].Content {
		t.Fatalf("immutable raw changed to %q: %v", durableRaw, err)
	}
	latest, err := store.Load(c.ID)
	if err != nil || latest.Revision != committedRevision {
		t.Fatalf("rejected retry advanced case: %+v err=%v", latest, err)
	}
}

func TestCommitPlanBundleCASConflictPublishesNothingFromLoser(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	c.ID = "task_plan_bundle_conflict"
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	winner, err := store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	loser, err := store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	winnerPlan, winnerHypotheses := transactionPlan("winner")
	loserPlan, loserHypotheses := transactionPlan("loser")
	preparePlannedCase(winner, winnerPlan)
	preparePlannedCase(loser, loserPlan)
	winnerEvidence := []domain.Evidence{planBundleEvidence(c.ID, "ev_bob_winner", "raw_bob_winner", "winner Bob fact")}
	loserEvidence := []domain.Evidence{planBundleEvidence(c.ID, "ev_bob_loser", "raw_bob_loser", "loser Bob fact")}
	winnerRaw := []RawRecord{{ID: "raw_bob_winner", Content: "winner raw"}}
	loserRaw := []RawRecord{{ID: "raw_bob_loser", Content: "loser raw"}}

	if err := store.CommitPlanBundle(winner, winnerPlan, winnerHypotheses, winnerEvidence, winnerRaw); err != nil {
		t.Fatal(err)
	}
	err = store.CommitPlanBundle(loser, loserPlan, loserHypotheses, loserEvidence, loserRaw)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("losing bundle error = %v, want revision conflict", err)
	}
	durablePlan, err := store.LoadPlan(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	durableHypotheses, err := store.Hypotheses(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	durableEvidence, err := store.Evidence(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(durablePlan, winnerPlan) || !reflect.DeepEqual(durableHypotheses, winnerHypotheses) || !reflect.DeepEqual(durableEvidence, winnerEvidence) {
		t.Fatalf("loser partially published: plan=%+v hypotheses=%+v evidence=%+v", durablePlan, durableHypotheses, durableEvidence)
	}
	if _, err := store.ReadRaw(c.ID, loserRaw[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("loser raw was published: %v", err)
	}
	if got, err := store.ReadRaw(c.ID, winnerRaw[0].ID); err != nil || got != winnerRaw[0].Content {
		t.Fatalf("winner raw = %q err=%v", got, err)
	}
	durableCase, err := store.Load(c.ID)
	if err != nil || durableCase.Revision != 2 {
		t.Fatalf("losing conflict advanced case: %+v err=%v", durableCase, err)
	}
}

func TestCommitPlanBundleContextCancellationAfterStagingPublishesNothing(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	c.ID = "task_plan_bundle_canceled"
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	plan, hypotheses := transactionPlan("canceled")
	preparePlannedCase(c, plan)
	evidence := []domain.Evidence{planBundleEvidence(c.ID, "ev_bob_canceled", "raw_bob_canceled", "canceled Bob fact")}
	raws := []RawRecord{{ID: "raw_bob_canceled", Content: "canceled raw"}}
	// Make cancellation become observable only at the final pre-publication
	// check, after input validation, lock acquisition, and staging have run.
	ctx := &cancelAfterErrChecks{Context: context.Background(), done: make(chan struct{})}

	err = store.CommitPlanBundleContext(ctx, c, plan, hypotheses, evidence, raws)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bundle error = %v, want context.Canceled", err)
	}
	durable, err := store.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Revision != 1 || durable.Status == domain.PhasePlanned {
		t.Fatalf("canceled bundle changed case: %+v", durable)
	}
	if _, err := store.LoadPlan(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled bundle published plan: %v", err)
	}
	if got, err := store.Evidence(c.ID); err != nil || len(got) != 0 {
		t.Fatalf("canceled bundle published evidence: %+v err=%v", got, err)
	}
	if _, err := store.ReadRaw(c.ID, raws[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled bundle published raw: %v", err)
	}
	taskDir, err := store.TaskDir(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	err = filepath.WalkDir(taskDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && (strings.Contains(entry.Name(), ".txn-") || strings.HasSuffix(entry.Name(), ".tmp")) {
			stages = append(stages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 0 {
		t.Fatalf("canceled bundle left staged files: %v", stages)
	}
	if _, err := os.Stat(filepath.Join(taskDir, transactionJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled bundle left transaction journal: %v", err)
	}
}

func TestWriteRawIsWriteOnceAndIdempotent(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := sampleCase()
	c.ID = "task_raw_immutable"
	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRaw(c.ID, "raw_stable", "same"); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRaw(c.ID, "raw_stable", "same"); err != nil {
		t.Fatalf("exact raw retry: %v", err)
	}
	if err := store.WriteRaw(c.ID, "raw_stable", "different"); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("raw mutation error = %v", err)
	}
	if got, err := store.ReadRaw(c.ID, "raw_stable"); err != nil || got != "same" {
		t.Fatalf("immutable raw = %q err=%v", got, err)
	}
}

func planBundleEvidence(taskID, evidenceID, rawID, claim string) domain.Evidence {
	return domain.Evidence{
		ID: evidenceID, Timestamp: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Kind: domain.KindRepositoryContract, Source: domain.Source{Tool: "bob", URI: "bob://fixture/v1"},
		Claim: claim, Confidence: domain.ConfidenceHigh,
		RawRef: "case://" + taskID + "/raw/" + rawID,
	}
}
