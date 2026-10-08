package kernel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

const (
	defaultJobFanout   = 5
	maxJobFanout       = 32
	jobStartGrace      = 2 * time.Minute
	jobHeartbeatStale  = 5 * time.Minute
	jobConflictRetries = 3

	// jobTokenEnv carries the per-job token from the queuing process to the
	// detached worker.
	jobTokenEnv = "CORTEX_JOB_TOKEN"
	// processStartSlack absorbs the one-second resolution of process start
	// times; a reused pid starts far later than this.
	processStartSlack = 2 * time.Second
)

// jobHeartbeatEvery is how often a running worker refreshes its heartbeat
// between rounds (a single round can outlast jobHeartbeatStale).
var jobHeartbeatEvery = 30 * time.Second

// Worker process hooks. Production uses the OS; tests replace them so no real
// process is inspected or signaled.
var (
	workerAlive     = casefs.ProcessAlive
	workerStartTime = processStartTime
	workerTerminate = terminateProcess
)

// workerState is what the kernel can prove about a job's recorded worker.
type workerState int

const (
	workerGone       workerState = iota // no such process
	workerSelf                          // the current process (in-process runner)
	workerConfirmed                     // alive and its start time matches the recorded one
	workerReused                        // alive, but started at a different time: the pid was reused
	workerUnverified                    // alive, identity cannot be compared
)

// jobSpawner starts a detached worker for a job and returns its pid. The
// default runs `cortex job run` as a new session so it outlives the MCP call
// that queued it; tests substitute an in-process runner.
type jobSpawner func(workspace, taskID, jobID, logPath string) (int, error)

// newJobToken returns a random per-job worker token.
func newJobToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate job token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// JobInput queues a detached investigation.
type JobInput struct {
	TaskID   string
	Question string
	Depth    string
	Modules  []string // explicit fan-out over modules (one bounded round each)
	Fanout   bool     // survey: fan out over the next unseen modules
	Max      int      // fan-out size (default 5, max 32)
}

// JobReport is the result of every job operation.
type JobReport struct {
	domain.Envelope
	Job     *domain.Job  `json:"job,omitempty"`
	Jobs    []domain.Job `json:"jobs,omitempty"`
	Running int          `json:"running"`
}

// SetJobSpawner replaces the detached worker launcher (tests).
func (k *Kernel) SetJobSpawner(s jobSpawner) { k.spawn = s }

func (k *Kernel) spawner(token string) jobSpawner {
	if k.spawn != nil {
		return k.spawn
	}
	return func(workspace, taskID, jobID, logPath string) (int, error) {
		return detachedSpawn(workspace, taskID, jobID, logPath, token)
	}
}

// detachedSpawn launches `cortex job run <task> <job>` in its own session with
// the log file as stdout/stderr, then releases the handle so the parent never
// waits on it.
func detachedSpawn(workspace, taskID, jobID, logPath, token string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locate cortex binary: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- store-built path inside the case directory
	if err != nil {
		return 0, err
	}
	defer func() { _ = logFile.Close() }()
	cmd := exec.Command(exe, "job", "run", taskID, jobID, "--workspace", workspace) // #nosec G204 -- fixed argv; ids validated by the store
	cmd.Stdout, cmd.Stderr, cmd.Stdin = logFile, logFile, nil
	cmd.Env = append(os.Environ(), jobTokenEnv+"="+token)
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// StartJob queues a detached investigation and launches its worker. The
// worker writes evidence through the ordinary Investigate path (redaction,
// budgets, junk filters), so a background round is indistinguishable from a
// foreground one in the ledger — only its wall clock is unbounded by MCP.
func (k *Kernel) StartJob(ctx context.Context, in JobInput) (JobReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return JobReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	if c.Status != domain.PhaseInvestigating && c.Status != domain.PhasePlanned {
		return JobReport{Envelope: k.errEnvelopeForCase(c, fmt.Sprintf("cannot start a background investigation in phase %q", c.Status))}, nil
	}
	depth, err := normalizeDepth(in.Depth)
	if err != nil {
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	question := strings.TrimSpace(in.Question)
	if textExceeds(question, maxRecordTextBytes) {
		return JobReport{Envelope: errEnvelope(c.ID, "job question exceeds the record bound")}, nil
	}
	modules := make([]string, 0, len(in.Modules))
	for _, m := range in.Modules {
		if m = strings.TrimSpace(m); m != "" {
			modules = append(modules, domain.NormalizeModulePath(m))
		}
	}
	if in.Fanout {
		ledger := k.coverageFor(c.ID)
		if ledger == nil {
			return JobReport{Envelope: k.errEnvelopeForCase(c, "fan-out needs a survey coverage ledger (open the case with --mode survey)")}, nil
		}
		max := in.Max
		if max <= 0 {
			max = defaultJobFanout
		}
		if max > maxJobFanout {
			max = maxJobFanout
		}
		for _, m := range ledger.Modules {
			if m.Status == domain.ModuleUnseen && len(modules) < max {
				modules = append(modules, m.Path)
			}
		}
		if len(modules) == 0 {
			return JobReport{Envelope: k.errEnvelopeForCase(c, "fan-out found no unseen modules; the survey ledger is fully explored")}, nil
		}
	}
	if len(modules) > maxJobFanout {
		return JobReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("a job fans out over at most %d modules", maxJobFanout))}, nil
	}
	if question == "" && len(modules) == 0 {
		return JobReport{Envelope: k.errEnvelopeActions(c.ID, "job needs a question, explicit modules, or fanout=true", domain.NextAction{
			Tool: "cortex_investigate", Command: cortexCommand(c, "investigate", c.ID, "QUESTION", "--async"),
			Reason: "queue a detached investigation", Arguments: cloneArgs(knownActionArgs(c), "async", true), Inputs: []string{"question"},
		})}, nil
	}
	if err := k.refuseRunningJobs(ctx, c); err != nil {
		return JobReport{Envelope: k.errEnvelopeForCase(c, err.Error())}, nil
	}
	now := k.now().UTC()
	token, err := newJobToken()
	if err != nil {
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	job := domain.Job{
		ID: ids.New("job"), Token: token, TaskID: c.ID, Kind: "investigate", Question: k.red.String(question), Depth: depth,
		Modules: modules, Status: domain.JobQueued, RoundsTotal: len(modules), CreatedAt: now,
	}
	if job.RoundsTotal == 0 {
		job.RoundsTotal = 1
	}
	logPath, err := k.store.JobLogPath(c.ID, job.ID)
	if err != nil {
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	job.LogRef = fmt.Sprintf("case://%s/jobs/%s", c.ID, job.ID)
	if err := k.store.AppendJob(c.ID, job); err != nil {
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	pid, spawnErr := k.spawner(token)(k.cfg.Workspace, c.ID, job.ID, logPath)
	// The freshly spawned child is certainly ours, so a start time recorded
	// now is a trustworthy identity; the worker re-records its own on start.
	var spawnedStart *time.Time
	if spawnErr == nil && pid > 0 && pid != os.Getpid() {
		if st, ok := workerStartTime(ctx, pid); ok {
			spawnedStart = &st
		}
	}
	updated, err := k.store.UpdateJob(c.ID, job.ID, func(j *domain.Job) error {
		if spawnErr != nil {
			j.Status, j.Error = domain.JobFailed, "could not start worker: "+spawnErr.Error()
			finished := k.now().UTC()
			j.FinishedAt = &finished
			return nil
		}
		if j.PID == 0 {
			j.PID = pid
			j.ProcessStart = spawnedStart
		}
		return nil
	})
	if err != nil {
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	if spawnErr != nil {
		return JobReport{Envelope: errEnvelope(c.ID, updated.Error), Job: &updated}, nil
	}
	rep := JobReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary:     fmt.Sprintf("background investigation %s queued (%d round(s)); evidence lands in this case as it completes", updated.ID, updated.RoundsTotal),
			NextActions: append([]string{"cortex job list " + c.ID + " — poll progress; cortex resume " + c.ID + " — read the checkpoint once it finishes"}, nextForPhase(c.Status)...)},
		Job: &updated, Running: 1,
	}
	rep.Actions = append(jobActions(c, updated), rep.Actions...)
	k.attachStructuredActions(&rep.Envelope, c)
	return rep, nil
}

func jobActions(c *domain.CaseFile, j domain.Job) []domain.NextAction {
	args := cloneArgs(knownActionArgs(c), "jobId", j.ID)
	out := []domain.NextAction{{
		Tool: "cortex_job", Command: cortexCommand(c, "job", "list", c.ID),
		Reason: "poll the detached investigation without blocking", Arguments: cloneArgs(args, "operation", "list"),
	}}
	if !j.Status.Terminal() {
		out = append(out, domain.NextAction{
			Tool: "cortex_job", Command: cortexCommand(c, "job", "cancel", c.ID, j.ID),
			Reason: "stop the worker; evidence already recorded is kept", Arguments: cloneArgs(args, "operation", "cancel"),
		})
	}
	return out
}

// RunJob is the worker entry point (`cortex job run`). It executes each
// bounded round through Investigate, records progress after every round, and
// finishes the job honestly: done, failed with the error, or canceled.
func (k *Kernel) RunJob(ctx context.Context, taskID, jobID string) error {
	jobs, err := k.store.Jobs(taskID)
	if err != nil {
		return err
	}
	var job *domain.Job
	for i := range jobs {
		if jobs[i].ID == jobID {
			job = &jobs[i]
		}
	}
	if job == nil {
		return fmt.Errorf("%w: %s", casefs.ErrJobNotFound, jobID)
	}
	if job.Status.Terminal() {
		return nil
	}
	// The queuing process hands the worker its token through the environment;
	// a mismatch means this invocation is not the one the record was made for.
	// An absent token (in-process runner, jobs queued by older versions) is
	// accepted.
	if want := os.Getenv(jobTokenEnv); want != "" && job.Token != "" && want != job.Token {
		return fmt.Errorf("job %s: worker token does not match the job record", jobID)
	}
	started := k.now().UTC()
	var selfStart *time.Time
	if st, ok := workerStartTime(ctx, os.Getpid()); ok {
		selfStart = &st
	}
	if _, err := k.store.UpdateJob(taskID, jobID, func(j *domain.Job) error {
		j.Status, j.PID, j.StartedAt, j.HeartbeatAt = domain.JobRunning, os.Getpid(), &started, &started
		j.ProcessStart = selfStart
		return nil
	}); err != nil {
		return err
	}
	stopBeat := k.beatJob(ctx, taskID, jobID)
	defer stopBeat()
	rounds := job.Modules
	if len(rounds) == 0 {
		rounds = []string{""}
	}
	var evidenceIDs []string
	var lastSummary string
	var runErr error
	done := 0
	for _, module := range rounds {
		if ctx.Err() != nil {
			runErr = ctx.Err()
			break
		}
		if current, err := k.store.Jobs(taskID); err == nil {
			for _, j := range current {
				if j.ID == jobID && j.Status == domain.JobCanceled {
					return nil
				}
			}
		}
		question := job.Question
		if question == "" {
			question = "responsibilities, entry points, and invariants of this module"
		}
		env, err := k.investigateWithRetry(ctx, InvestigateInput{TaskID: taskID, Question: question, Depth: job.Depth, Module: module})
		if err != nil {
			runErr = err
			break
		}
		if !env.OK {
			runErr = errors.New(env.Error)
			break
		}
		for _, f := range env.Facts {
			evidenceIDs = append(evidenceIDs, f.ID)
		}
		lastSummary = env.Summary
		done++
		beat := k.now().UTC()
		if _, err := k.store.UpdateJob(taskID, jobID, func(j *domain.Job) error {
			j.RoundsDone, j.HeartbeatAt = done, &beat
			j.EvidenceIDs = append([]string(nil), evidenceIDs...)
			j.Summary = lastSummary
			return nil
		}); err != nil {
			runErr = err
			break
		}
	}
	finished := k.now().UTC()
	_, err = k.store.UpdateJob(taskID, jobID, func(j *domain.Job) error {
		if j.Status == domain.JobCanceled {
			return nil
		}
		j.FinishedAt = &finished
		j.RoundsDone = done
		j.EvidenceIDs = append([]string(nil), evidenceIDs...)
		if runErr != nil {
			j.Status, j.Error = domain.JobFailed, k.red.String(runErr.Error())
			return nil
		}
		j.Status = domain.JobDone
		j.Summary = fmt.Sprintf("%d round(s) completed, %d evidence record(s) written", done, len(evidenceIDs))
		return nil
	})
	k.checkpoint(taskID)
	if err != nil {
		return err
	}
	return runErr
}

// beatJob refreshes the running job's heartbeat on a timer so a long round
// does not look like a stalled worker. The returned func stops it and waits.
// Only the process recorded as the worker may beat, and only while the job is
// still running, so a repaired or canceled record is never resurrected.
func (k *Kernel) beatJob(ctx context.Context, taskID, jobID string) func() {
	beatCtx, cancel := context.WithCancel(ctx)
	now, every, self := k.now, jobHeartbeatEvery, os.Getpid()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-ticker.C:
				beat := now().UTC()
				_, _ = k.store.UpdateJob(taskID, jobID, func(j *domain.Job) error {
					if j.Status == domain.JobRunning && j.PID == self {
						j.HeartbeatAt = &beat
					}
					return nil
				})
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

// investigateWithRetry reloads and retries an investigation round when a
// concurrent foreground call won the case snapshot CAS.
func (k *Kernel) investigateWithRetry(ctx context.Context, in InvestigateInput) (domain.Envelope, error) {
	var env domain.Envelope
	var err error
	for attempt := 0; attempt < jobConflictRetries; attempt++ {
		env, err = k.Investigate(ctx, in)
		if err == nil || !errors.Is(err, casefs.ErrRevisionConflict) {
			return env, err
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	return env, err
}

// ListJobs returns the task's jobs, repairing dead workers on read: a
// queued/running job whose process is gone is failed, never left "running".
func (k *Kernel) ListJobs(ctx context.Context, taskID string) (JobReport, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return JobReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	jobs, err := k.repairedJobsCtx(ctx, c.ID)
	if err != nil {
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	rep := JobReport{Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status}, Jobs: jobs}
	for _, j := range jobs {
		if !j.Status.Terminal() {
			rep.Running++
			rep.Actions = append(rep.Actions, jobActions(c, j)[1])
		}
	}
	rep.Summary = fmt.Sprintf("%d job(s), %d in flight", len(jobs), rep.Running)
	return rep, nil
}

// inspectWorker establishes what is provably true about a job's recorded
// worker. A pid alone is never trusted: after a crash the OS may hand the
// number to an unrelated process, so a live pid counts as the worker only when
// its start time matches the one recorded at spawn/startup.
func (k *Kernel) inspectWorker(ctx context.Context, j domain.Job) workerState {
	switch {
	case j.PID <= 0:
		return workerGone
	case j.PID == os.Getpid():
		return workerSelf
	case !workerAlive(j.PID):
		return workerGone
	case j.ProcessStart == nil:
		return workerUnverified
	}
	current, ok := workerStartTime(ctx, j.PID)
	if !ok {
		return workerUnverified
	}
	if current.Sub(*j.ProcessStart).Abs() <= processStartSlack {
		return workerConfirmed
	}
	return workerReused
}

// workerHeartbeatFresh reports whether an unverifiable worker has reported in
// recently enough to be left alone: a queued job gets the start grace from
// creation, a running one the heartbeat bound from its last beat.
func workerHeartbeatFresh(j domain.Job, now time.Time) bool {
	switch {
	case j.HeartbeatAt != nil:
		return now.Sub(*j.HeartbeatAt) <= jobHeartbeatStale
	case j.StartedAt != nil:
		return now.Sub(*j.StartedAt) <= jobHeartbeatStale
	default:
		return now.Sub(j.CreatedAt) <= jobStartGrace
	}
}

// repairReason returns why a non-terminal job must be failed, or "" when its
// worker is (or may still be) alive.
func (k *Kernel) repairReason(ctx context.Context, j domain.Job, now time.Time) string {
	if j.PID <= 0 {
		if j.Status == domain.JobQueued && now.Sub(j.CreatedAt) > jobStartGrace {
			return "worker never started"
		}
		return ""
	}
	switch k.inspectWorker(ctx, j) {
	case workerGone:
		return "worker process exited before finishing"
	case workerReused:
		return "worker process exited before finishing (its pid was reused by an unrelated process)"
	case workerUnverified:
		if !workerHeartbeatFresh(j, now) {
			return "worker identity could not be confirmed and it stopped reporting progress"
		}
	}
	return ""
}

func (k *Kernel) repairedJobsCtx(ctx context.Context, taskID string) ([]domain.Job, error) {
	jobs, err := k.store.Jobs(taskID)
	if err != nil {
		return nil, err
	}
	now := k.now().UTC()
	for i, j := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if j.Status.Terminal() {
			continue
		}
		reason := k.repairReason(ctx, j, now)
		if reason == "" {
			continue
		}
		repaired, err := k.store.UpdateJob(taskID, j.ID, func(j *domain.Job) error {
			if j.Status.Terminal() {
				return nil
			}
			j.Status, j.Error, j.FinishedAt = domain.JobFailed, reason, &now
			return nil
		})
		if err == nil {
			jobs[i] = repaired
		}
	}
	return jobs, nil
}

// CancelJob marks a job canceled and terminates its worker, but only when the
// worker's identity is confirmed. An unconfirmed pid is never signaled; the
// worker still stops on its own because it checks for cancellation between
// rounds.
func (k *Kernel) CancelJob(ctx context.Context, taskID, jobID string) (JobReport, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return JobReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	now := k.now().UTC()
	var snapshot domain.Job
	updated, err := k.store.UpdateJob(c.ID, strings.TrimSpace(jobID), func(j *domain.Job) error {
		if j.Status.Terminal() {
			return fmt.Errorf("job %s is already %s", j.ID, j.Status)
		}
		snapshot = *j
		j.Status, j.FinishedAt = domain.JobCanceled, &now
		return nil
	})
	if err != nil {
		if errors.Is(err, casefs.ErrJobNotFound) {
			return JobReport{Envelope: k.errEnvelopeActions(c.ID, err.Error(), domain.NextAction{
				Tool: "cortex_job", Command: cortexCommand(c, "job", "list", c.ID), Reason: "list this case's jobs",
				Arguments: cloneArgs(knownActionArgs(c), "operation", "list")})}, nil
		}
		return JobReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	var warnings []string
	switch k.inspectWorker(ctx, snapshot) {
	case workerConfirmed:
		if err := workerTerminate(snapshot.PID); err != nil {
			warnings = append(warnings, "job marked canceled but the worker could not be signaled: "+err.Error())
		}
	case workerReused:
		warnings = append(warnings, "job marked canceled; worker identity could not be confirmed (its pid now belongs to another process); not signaled")
	case workerUnverified:
		warnings = append(warnings, "job marked canceled; worker identity could not be confirmed; not signaled (a live worker stops after its current round)")
	}
	return JobReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status, Summary: fmt.Sprintf("job %s canceled", updated.ID), Warnings: warnings},
		Job:      &updated,
	}, nil
}

// refuseRunningJobs keeps one detached worker per case (their rounds share
// the case snapshot) and blocks completion while one is in flight.
func (k *Kernel) refuseRunningJobs(ctx context.Context, c *domain.CaseFile) error {
	jobs, err := k.repairedJobsCtx(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if !j.Status.Terminal() {
			return fmt.Errorf("background job %s is still %s (%d/%d rounds); wait for it or cancel it with cortex job cancel", j.ID, j.Status, j.RoundsDone, j.RoundsTotal)
		}
	}
	return nil
}
