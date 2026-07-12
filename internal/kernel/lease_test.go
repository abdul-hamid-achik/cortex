package kernel

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

func sharedLeaseKernels(t *testing.T) (*Kernel, *Kernel) {
	t.Helper()
	t.Setenv("CORTEX_HOME", t.TempDir())
	cfg := config.For(testRepo(t))
	s1, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	return NewWith(cfg, s1, adapters.NewRegistry()), NewWith(cfg, s2, adapters.NewRegistry())
}

func plannedChangeTask(t *testing.T, k *Kernel) string {
	t.Helper()
	started, err := k.StartTask(context.Background(), StartInput{Goal: "coordinate a repair"})
	if err != nil || !started.OK {
		t.Fatalf("start: err=%v envelope=%+v", err, started)
	}
	planned, err := k.Plan(PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "the callback is wrong", DisproveBy: "inspect the callback test",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "the edge-case behavior is not yet proven",
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan: err=%v envelope=%+v", err, planned)
	}
	return started.TaskID
}

func TestChangeLeaseAcquireRenewRelease(t *testing.T) {
	k, _ := sharedLeaseKernels(t)
	taskID := plannedChangeTask(t, k)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	k.now = func() time.Time { return now }

	acquired, err := k.AcquireChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-a", TTL: 2 * time.Minute})
	if err != nil || !acquired.OK {
		t.Fatalf("acquire: err=%v envelope=%+v", err, acquired)
	}
	first, _ := k.Store().Load(taskID)
	if first.ChangeLease == nil || first.ChangeLease.Actor != "agent-a" || !first.ChangeLease.ExpiresAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("bad acquired lease: %+v", first.ChangeLease)
	}
	revision := first.Revision

	wrong, _ := k.RenewChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-b", TTL: time.Minute})
	if wrong.OK || !strings.Contains(wrong.Error, "belongs to") {
		t.Fatalf("another actor renewed lease: %+v", wrong)
	}
	unchanged, _ := k.Store().Load(taskID)
	if unchanged.Revision != revision {
		t.Fatalf("rejected renewal changed revision: got %d want %d", unchanged.Revision, revision)
	}

	now = now.Add(time.Minute)
	renewed, err := k.RenewChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-a", TTL: 10 * time.Minute})
	if err != nil || !renewed.OK {
		t.Fatalf("renew: err=%v envelope=%+v", err, renewed)
	}
	afterRenew, _ := k.Store().Load(taskID)
	if !afterRenew.ChangeLease.AcquiredAt.Equal(first.ChangeLease.AcquiredAt) ||
		!afterRenew.ChangeLease.RenewedAt.Equal(now) || !afterRenew.ChangeLease.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("bad renewed lease: %+v", afterRenew.ChangeLease)
	}

	wrong, _ = k.ReleaseChangeLease(ReleaseChangeLeaseInput{TaskID: taskID, Actor: "agent-b"})
	if wrong.OK || !strings.Contains(wrong.Error, "belongs to") {
		t.Fatalf("another actor released lease: %+v", wrong)
	}
	released, err := k.ReleaseChangeLease(ReleaseChangeLeaseInput{TaskID: taskID, Actor: "agent-a"})
	if err != nil || !released.OK {
		t.Fatalf("release: err=%v envelope=%+v", err, released)
	}
	afterRelease, _ := k.Store().Load(taskID)
	if afterRelease.ChangeLease.ReleasedAt == nil || afterRelease.ChangeLease.Active(now) {
		t.Fatalf("lease not released: %+v", afterRelease.ChangeLease)
	}

	reacquired, _ := k.AcquireChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-b", TTL: time.Minute})
	if !reacquired.OK {
		t.Fatalf("released lease was not recoverable: %+v", reacquired)
	}
	latest, _ := k.Store().Load(taskID)
	if latest.ChangeLease.Actor != "agent-b" || latest.ChangeLease.ReleasedAt != nil {
		t.Fatalf("replacement lease not installed: %+v", latest.ChangeLease)
	}
}

func TestChangeLeaseStaleRecovery(t *testing.T) {
	k, _ := sharedLeaseKernels(t)
	taskID := plannedChangeTask(t, k)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	k.now = func() time.Time { return now }
	got, _ := k.AcquireChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-a", TTL: time.Second})
	if !got.OK {
		t.Fatalf("acquire: %+v", got)
	}
	now = now.Add(time.Second)
	got, _ = k.RenewChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-a", TTL: time.Minute})
	if got.OK || !strings.Contains(got.Error, "expired") {
		t.Fatalf("expired lease renewed: %+v", got)
	}
	got, _ = k.AcquireChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: "agent-b", TTL: time.Minute})
	if !got.OK {
		t.Fatalf("stale lease was not recoverable: %+v", got)
	}
	c, _ := k.Store().Load(taskID)
	if c.ChangeLease.Actor != "agent-b" || !c.ChangeLease.AcquiredAt.Equal(now) {
		t.Fatalf("stale lease was not replaced: %+v", c.ChangeLease)
	}
}

func TestConcurrentChangeLeaseAcquireHasOneWinner(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	taskID := plannedChangeTask(t, k1)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	k1.now = func() time.Time { return now }
	k2.now = func() time.Time { return now }

	start := make(chan struct{})
	results := make(chan domain.Envelope, 2)
	var wg sync.WaitGroup
	for _, attempt := range []struct {
		kernel *Kernel
		actor  string
	}{{k1, "agent-a"}, {k2, "agent-b"}} {
		wg.Add(1)
		go func(k *Kernel, actor string) {
			defer wg.Done()
			<-start
			env, _ := k.AcquireChangeLease(ChangeLeaseInput{TaskID: taskID, Actor: actor, TTL: time.Minute})
			results <- env
		}(attempt.kernel, attempt.actor)
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for result := range results {
		if result.OK {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent lease winners = %d, want 1", winners)
	}
	c, err := k1.Store().Load(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if c.ChangeLease == nil || !c.ChangeLease.Active(now) || (c.ChangeLease.Actor != "agent-a" && c.ChangeLease.Actor != "agent-b") {
		t.Fatalf("winning lease missing: %+v", c.ChangeLease)
	}
}

func TestChangeLeaseGuards(t *testing.T) {
	k, _ := sharedLeaseKernels(t)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "not planned"})
	got, _ := k.AcquireChangeLease(ChangeLeaseInput{TaskID: started.TaskID, Actor: "agent", TTL: time.Minute})
	if got.OK || !strings.Contains(got.Error, "plan the task first") {
		t.Fatalf("unplanned change acquired lease: %+v", got)
	}

	taskID := plannedChangeTask(t, k)
	for _, input := range []ChangeLeaseInput{
		{TaskID: taskID, Actor: "", TTL: time.Minute},
		{TaskID: taskID, Actor: "agent", TTL: time.Millisecond},
		{TaskID: taskID, Actor: "agent", TTL: MaxChangeLeaseTTL + time.Second},
		{TaskID: taskID, Actor: "agent; rm", TTL: time.Minute},
		{TaskID: taskID, Actor: "ghp_16C7e42F292c6912E7710c838347Ae178B4a99", TTL: time.Minute},
	} {
		got, _ := k.AcquireChangeLease(input)
		if got.OK {
			t.Errorf("invalid lease input accepted: %+v", input)
		}
	}
}
