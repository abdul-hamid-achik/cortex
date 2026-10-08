package kernel

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const fakeWorkerPID = 424242

// fakeWorker replaces the process hooks so no real process is inspected or
// signaled. start==nil models a platform where start times are unavailable.
type fakeWorker struct {
	alive      bool
	start      *time.Time
	terminated []int
}

func installFakeWorker(t *testing.T, w *fakeWorker) {
	t.Helper()
	oldAlive, oldStart, oldTerm := workerAlive, workerStartTime, workerTerminate
	workerAlive = func(pid int) bool { return w.alive && pid == fakeWorkerPID }
	workerStartTime = func(_ context.Context, pid int) (time.Time, bool) {
		if pid != fakeWorkerPID || w.start == nil {
			return time.Time{}, false
		}
		return *w.start, true
	}
	workerTerminate = func(pid int) error { w.terminated = append(w.terminated, pid); return nil }
	t.Cleanup(func() { workerAlive, workerStartTime, workerTerminate = oldAlive, oldStart, oldTerm })
}

// queueFakeJob queues a job whose "worker" is the fake pid.
func queueFakeJob(t *testing.T, k *Kernel, taskID string) domain.Job {
	t.Helper()
	k.SetJobSpawner(func(workspace, taskID, jobID, logPath string) (int, error) { return fakeWorkerPID, nil })
	rep, _ := k.StartJob(context.Background(), JobInput{TaskID: taskID, Question: "q"})
	if !rep.OK || rep.Job == nil || rep.Job.PID != fakeWorkerPID {
		t.Fatalf("queue job: %+v", rep.Envelope)
	}
	return *rep.Job
}

func jobKernel(t *testing.T) (*Kernel, string) {
	t.Helper()
	k := newTestKernel(t, testRepo(t), vecgrepFake("src/callback.go"))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Mode: domain.ModeInvestigate})
	if !env.OK {
		t.Fatalf("start: %+v", env)
	}
	return k, env.TaskID
}

func jobByID(t *testing.T, k *Kernel, taskID, jobID string) domain.Job {
	t.Helper()
	jobs, err := k.Store().Jobs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID == jobID {
			return j
		}
	}
	t.Fatalf("job %s missing", jobID)
	return domain.Job{}
}

func TestStartJobRecordsTokenAndSpawnStartTime(t *testing.T) {
	k, id := jobKernel(t)
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	installFakeWorker(t, &fakeWorker{alive: true, start: &start})
	job := queueFakeJob(t, k, id)
	if len(job.Token) < 32 {
		t.Errorf("job should carry a random token: %q", job.Token)
	}
	if job.ProcessStart == nil || !job.ProcessStart.Equal(start) {
		t.Errorf("spawn-time start = %v, want %v", job.ProcessStart, start)
	}
}

func TestReusedPidIsRepairedAndNeverSignaled(t *testing.T) {
	k, id := jobKernel(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	w := &fakeWorker{alive: true, start: &start}
	installFakeWorker(t, w)

	// Identity mismatch repairs to failed.
	job := queueFakeJob(t, k, id)
	later := start.Add(30 * time.Minute) // another process now owns the pid
	w.start = &later
	list, _ := k.ListJobs(ctx, id)
	got := jobByID(t, k, id, job.ID)
	if got.Status != domain.JobFailed || !strings.Contains(got.Error, "reused") || list.Running != 0 {
		t.Fatalf("reused pid should fail the job: %+v running=%d", got, list.Running)
	}
	if len(w.terminated) != 0 {
		t.Fatalf("repair must never signal: %v", w.terminated)
	}

	// Cancel with a mismatched identity: canceled, warned, not signaled.
	w.start = &start
	job2 := queueFakeJob(t, k, id)
	w.start = &later
	rep, _ := k.CancelJob(ctx, id, job2.ID)
	if !rep.OK || rep.Job.Status != domain.JobCanceled {
		t.Fatalf("cancel: %+v", rep.Envelope)
	}
	if len(w.terminated) != 0 {
		t.Fatalf("mismatched identity was signaled: %v", w.terminated)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "worker identity could not be confirmed") || !strings.Contains(rep.Warnings[0], "not signaled") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
}

func TestConfirmedWorkerIsCanceledAndSignaled(t *testing.T) {
	k, id := jobKernel(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	w := &fakeWorker{alive: true, start: &start}
	installFakeWorker(t, w)
	job := queueFakeJob(t, k, id)

	// A confirmed live worker is left alone by repair.
	list, _ := k.ListJobs(ctx, id)
	if list.Running != 1 || jobByID(t, k, id, job.ID).Status != domain.JobQueued {
		t.Fatalf("confirmed worker must stay in flight: %+v", list.Jobs)
	}
	rep, _ := k.CancelJob(ctx, id, job.ID)
	if !rep.OK || rep.Job.Status != domain.JobCanceled || len(rep.Warnings) != 0 {
		t.Fatalf("cancel: %+v warnings=%v", rep.Envelope, rep.Warnings)
	}
	if len(w.terminated) != 1 || w.terminated[0] != fakeWorkerPID {
		t.Fatalf("confirmed worker should be signaled once: %v", w.terminated)
	}
}

func TestUnverifiableWorkerFollowsHeartbeat(t *testing.T) {
	k, id := jobKernel(t)
	ctx := context.Background()
	w := &fakeWorker{alive: true} // no start time available anywhere
	installFakeWorker(t, w)
	job := queueFakeJob(t, k, id)
	if job.ProcessStart != nil {
		t.Fatalf("no start time should be recorded: %v", job.ProcessStart)
	}
	now := k.now().UTC()
	setRunning := func(beat time.Time) {
		t.Helper()
		if _, err := k.Store().UpdateJob(id, job.ID, func(j *domain.Job) error {
			j.Status, j.StartedAt, j.HeartbeatAt = domain.JobRunning, &beat, &beat
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Fresh heartbeat: left alone, but cancel must not signal an unverified pid.
	setRunning(now.Add(-time.Minute))
	if list, _ := k.ListJobs(ctx, id); list.Running != 1 {
		t.Fatalf("fresh heartbeat should keep the job in flight: %+v", list.Jobs)
	}
	rep, _ := k.CancelJob(ctx, id, job.ID)
	if !rep.OK || len(w.terminated) != 0 || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "not signaled") {
		t.Fatalf("unverified cancel: ok=%v warnings=%v signaled=%v", rep.OK, rep.Warnings, w.terminated)
	}

	// Stale heartbeat: repaired to failed even though the pid is alive.
	job2 := queueFakeJob(t, k, id)
	job = job2
	setRunning(now.Add(-jobHeartbeatStale - time.Minute))
	list, _ := k.ListJobs(ctx, id)
	got := jobByID(t, k, id, job2.ID)
	if got.Status != domain.JobFailed || !strings.Contains(got.Error, "identity could not be confirmed") || list.Running != 0 {
		t.Fatalf("stale heartbeat should fail the job: %+v", got)
	}
	if len(w.terminated) != 0 {
		t.Fatalf("repair must never signal: %v", w.terminated)
	}
}

func TestRunJobRecordsOwnIdentityAndChecksToken(t *testing.T) {
	k, id := jobKernel(t)
	ctx := context.Background()
	self := time.Now().Add(-time.Hour).Truncate(time.Second)
	oldStart := workerStartTime
	workerStartTime = func(_ context.Context, pid int) (time.Time, bool) { return self, pid == os.Getpid() }
	t.Cleanup(func() { workerStartTime = oldStart })
	k.SetJobSpawner(func(workspace, taskID, jobID, logPath string) (int, error) { return os.Getpid(), nil })
	rep, _ := k.StartJob(ctx, JobInput{TaskID: id, Question: "callback"})
	if !rep.OK {
		t.Fatalf("start: %+v", rep.Envelope)
	}

	t.Setenv(jobTokenEnv, "not-the-token")
	if err := k.RunJob(ctx, id, rep.Job.ID); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("a mismatched token must be refused, got %v", err)
	}
	if got := jobByID(t, k, id, rep.Job.ID); got.Status != domain.JobQueued {
		t.Fatalf("a refused worker must not touch the record: %+v", got)
	}

	t.Setenv(jobTokenEnv, rep.Job.Token)
	jobHeartbeatEvery = time.Millisecond
	t.Cleanup(func() { jobHeartbeatEvery = 30 * time.Second })
	if err := k.RunJob(ctx, id, rep.Job.ID); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := jobByID(t, k, id, rep.Job.ID)
	if got.Status != domain.JobDone || got.PID != os.Getpid() || got.ProcessStart == nil || !got.ProcessStart.Equal(self) {
		t.Errorf("worker should record its identity: %+v", got)
	}
}

func TestCancelledContextStopsJobRepair(t *testing.T) {
	k, id := jobKernel(t)
	installFakeWorker(t, &fakeWorker{alive: true})
	queueFakeJob(t, k, id)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := k.ListJobs(ctx, id); err == nil {
		t.Fatal("a canceled context should surface from ListJobs")
	}
}
