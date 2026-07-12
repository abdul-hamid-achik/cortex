package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestBeginChangeAcquiresLeaseAndGuardsVerification(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "coordinate change", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	begun, err := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-a", TTL: time.Minute})
	if err != nil || !begun.OK || begun.Phase != domain.PhaseChanging {
		t.Fatalf("begin = %+v err=%v", begun, err)
	}
	again, _ := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-a", TTL: time.Minute})
	if !again.OK || !strings.Contains(again.Summary, "already") {
		t.Fatalf("same-owner retry is not idempotent: %+v", again)
	}
	other, _ := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-b", TTL: time.Minute})
	if other.OK {
		t.Fatalf("second actor began owned change: %+v", other)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 11 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrong, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, Actor: "agent-b"})
	if wrong.OK || !strings.Contains(wrong.Error, "belongs") {
		t.Fatalf("non-owner verified leased change: %+v", wrong)
	}
	secretActor, _ := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, Actor: "ghp_16C7e42F292c6912E7710c838347Ae178B4a99",
	})
	if secretActor.OK || strings.Contains(secretActor.Error, "ghp_") {
		t.Fatalf("secret-shaped verification actor was accepted or reflected: %+v", secretActor)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, Actor: "agent-a"})
	if !verified.OK || verified.Phase != domain.PhaseVerifying {
		t.Fatalf("owner verify = %+v", verified)
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	for _, receipt := range receipts {
		if receipt.Actor != "agent-a" {
			t.Fatalf("verification receipt lost actor provenance: %+v", receipt)
		}
	}
	completed, _ := k.Remember(context.Background(), RememberInput{
		TaskID: started.TaskID, Outcome: "done", VerificationNotPossible: true,
	})
	if !completed.OK {
		t.Fatalf("remember = %+v", completed)
	}
	c, _ := k.Store().Load(started.TaskID)
	if c.ChangeLease == nil || c.ChangeLease.ReleasedAt == nil {
		t.Fatalf("completion did not release lease: %+v", c.ChangeLease)
	}
}

func TestConcurrentBeginChangeBySameActorIsIdempotent(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	taskID := plannedChangeTask(t, k1)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	k1.now = func() time.Time { return now }
	k2.now = func() time.Time { return now }
	start := make(chan struct{})
	results := make(chan domain.Envelope, 2)
	var wg sync.WaitGroup
	for _, k := range []*Kernel{k1, k2} {
		wg.Add(1)
		go func(k *Kernel) {
			defer wg.Done()
			<-start
			env, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a", TTL: time.Minute})
			results <- env
		}(k)
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if !result.OK || result.Phase != domain.PhaseChanging {
			t.Fatalf("same-owner begin did not converge: %+v", result)
		}
	}
	c, _ := k1.Store().Load(taskID)
	if c.Status != domain.PhaseChanging || c.ChangeLease == nil || c.ChangeLease.Actor != "agent-a" {
		t.Fatalf("final change ownership = %+v", c)
	}
	events, _ := k1.Store().PhaseEvents(taskID)
	changing := 0
	for _, event := range events {
		if event.To == domain.PhaseChanging {
			changing++
		}
	}
	if changing != 1 {
		t.Fatalf("changing transition recorded %d times: %+v", changing, events)
	}
}

func TestExpiredLeaseMustBeReacquiredBeforeVerification(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	k.now = func() time.Time { return now }
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "repair under explicit ownership", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	begun, _ := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-a", TTL: time.Second})
	if !begun.OK {
		t.Fatalf("begin = %+v", begun)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 22 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)

	stale, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, Actor: "agent-a"})
	if stale.OK || !strings.Contains(stale.Error, "expired") || !strings.Contains(stale.Error, "begin-change") {
		t.Fatalf("expired owner verified without reacquiring: %+v", stale)
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if len(status.Actions) != 1 || status.Actions[0].Tool != "cortex_begin_change" {
		t.Fatalf("expired ownership actions = %+v, want begin-change", status.Actions)
	}

	reacquired, _ := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-b", TTL: time.Minute})
	if !reacquired.OK || reacquired.Phase != domain.PhaseChanging {
		t.Fatalf("reacquire = %+v", reacquired)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, Actor: "agent-b"})
	if !verified.OK {
		t.Fatalf("new owner could not verify: %+v", verified)
	}
}

func TestBeginChangeReturnsFailedVerificationToChanging(t *testing.T) {
	k, _ := sharedLeaseKernels(t)
	taskID := plannedChangeTask(t, k)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	k.now = func() time.Time { return now }
	if begun, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a", TTL: time.Minute}); !begun.OK {
		t.Fatalf("begin = %+v", begun)
	}
	c, err := k.Store().Load(taskID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = domain.PhaseVerifying
	if err := k.Store().Save(c); err != nil {
		t.Fatal(err)
	}

	repair, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a"})
	if !repair.OK || repair.Phase != domain.PhaseChanging || !strings.Contains(repair.Summary, "repair") {
		t.Fatalf("verification repair = %+v", repair)
	}
}

func TestReleasedLeaseMustBeReacquiredBeforeVerification(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	releasedAt := now.Add(-time.Second)
	c := &domain.CaseFile{ChangeLease: &domain.ChangeLease{
		Actor: "agent-a", AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Minute), ReleasedAt: &releasedAt,
	}}
	err := validateVerificationLease(c, "agent-a", now)
	if err == nil || !strings.Contains(err.Error(), "released") || !strings.Contains(err.Error(), "begin-change") {
		t.Fatalf("released lease was accepted for verification: %v", err)
	}
}
