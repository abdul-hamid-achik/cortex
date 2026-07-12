package kernel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestOpenTaskIsIdempotentByKeyAndGoal(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	first, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "  Fix   Redirect ", IdempotencyKey: "run-123", Actor: "agent-a",
	}})
	if err != nil || !first.OK {
		t.Fatalf("first open = %+v err=%v", first, err)
	}
	retry, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "different retry text", IdempotencyKey: "run-123", Actor: "agent-a",
	}})
	if retry.TaskID != first.TaskID || retry.Phase != domain.PhaseInvestigating {
		t.Fatalf("key retry created another task: first=%+v retry=%+v", first, retry)
	}
	goalRetry, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "fix redirect"}})
	if goalRetry.TaskID != first.TaskID {
		t.Fatalf("normalized active goal did not resume: first=%s retry=%s", first.TaskID, goalRetry.TaskID)
	}
	ids, _ := k.Store().List()
	if len(ids) != 1 {
		t.Fatalf("idempotent opens created %d tasks: %v", len(ids), ids)
	}
}

func TestOpenTaskRecoversPersistedStartSkeleton(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	c := &domain.CaseFile{
		SchemaVersion: domain.SchemaVersion, ID: "task_recover", CreatedAt: time.Now().UTC(),
		Goal: "recover me", Mode: domain.ModeChange, Status: domain.PhaseNew, Risk: "low",
		Surfaces: []domain.Surface{domain.SurfaceCode}, IdempotencyKey: "recover-key",
		Workspace: domain.Workspace{Root: k.cfg.Workspace, Repository: "repo"},
	}
	if err := k.Store().Create(c); err != nil {
		t.Fatal(err)
	}
	result, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "recover me", IdempotencyKey: "recover-key",
	}})
	if err != nil || !result.OK || result.TaskID != c.ID || result.Phase != domain.PhaseInvestigating {
		t.Fatalf("recovery = %+v err=%v", result, err)
	}
}

func TestOpenTaskLinksParentAndChild(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	parent, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "parent work"}})
	child, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "child work", ParentTaskID: parent.TaskID, Actor: "agent-child",
	}})
	if !child.OK {
		t.Fatalf("child open = %+v", child)
	}
	parentCase, _ := k.Store().Load(parent.TaskID)
	childCase, _ := k.Store().Load(child.TaskID)
	if childCase.ParentTaskID != parent.TaskID || len(parentCase.ChildTaskIDs) != 1 || parentCase.ChildTaskIDs[0] != child.TaskID {
		t.Fatalf("linkage missing: parent=%+v child=%+v", parentCase, childCase)
	}
}

func TestOpenTaskGoalDeduplicationKeepsParentIdentity(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	parent, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "same goal"}})
	child, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "same goal", ParentTaskID: parent.TaskID, Actor: "agent-child",
	}})
	if !child.OK || child.TaskID == parent.TaskID {
		t.Fatalf("child request resumed its same-goal parent: parent=%+v child=%+v", parent, child)
	}
	childRetry, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: " same   goal ", ParentTaskID: parent.TaskID, Actor: "another-agent",
	}})
	if childRetry.TaskID != child.TaskID {
		t.Fatalf("same-parent goal retry did not resume child: first=%s retry=%s", child.TaskID, childRetry.TaskID)
	}
}

func TestOpenTaskRetryRepairsParentLinkage(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	parent, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "parent work"}})
	child, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "child work", ParentTaskID: parent.TaskID, IdempotencyKey: "child-once", Actor: "agent-child",
	}})
	parentCase, _ := k.Store().Load(parent.TaskID)
	parentCase.ChildTaskIDs = nil // simulate the second CAS write being lost/interrupted
	if err := k.Store().Save(parentCase); err != nil {
		t.Fatal(err)
	}
	retry, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "retry child", ParentTaskID: parent.TaskID, IdempotencyKey: "child-once", Actor: "agent-child",
	}})
	if err != nil || !retry.OK || retry.TaskID != child.TaskID {
		t.Fatalf("child retry = %+v (%v)", retry, err)
	}
	parentCase, _ = k.Store().Load(parent.TaskID)
	if len(parentCase.ChildTaskIDs) != 1 || parentCase.ChildTaskIDs[0] != child.TaskID {
		t.Fatalf("parent linkage was not repaired: %+v", parentCase.ChildTaskIDs)
	}
}

func TestOpenTaskRejectsUnknownParent(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	result, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "child", ParentTaskID: "task_missing"}})
	if result.OK {
		t.Fatalf("unknown parent accepted: %+v", result)
	}
}

func TestOpenTaskConcurrentIdempotencyKeyCreatesOneCase(t *testing.T) {
	k1, k2 := sharedLeaseKernels(t)
	start := make(chan struct{})
	results := make(chan domain.Envelope, 2)
	var wg sync.WaitGroup
	for _, k := range []*Kernel{k1, k2} {
		wg.Add(1)
		go func(k *Kernel) {
			defer wg.Done()
			<-start
			env, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
				Goal: "open once across stores", IdempotencyKey: "run-concurrent", Actor: "agent",
			}})
			results <- env
		}(k)
	}
	close(start)
	wg.Wait()
	close(results)
	taskID := ""
	for result := range results {
		if !result.OK {
			t.Fatalf("concurrent open failed: %+v", result)
		}
		if taskID == "" {
			taskID = result.TaskID
		} else if result.TaskID != taskID {
			t.Fatalf("concurrent opens returned different tasks: %s and %s", taskID, result.TaskID)
		}
	}
	ids, err := k1.Store().List()
	if err != nil || len(ids) != 1 || ids[0] != taskID {
		t.Fatalf("case list = %v (%v), want only %s", ids, err, taskID)
	}
}
