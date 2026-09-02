package domain

import "time"

// JobStatus is the lifecycle of one detached background job.
type JobStatus string

const (
	JobQueued   JobStatus = "queued"
	JobRunning  JobStatus = "running"
	JobDone     JobStatus = "done"
	JobFailed   JobStatus = "failed"
	JobCanceled JobStatus = "canceled"
)

// Terminal reports whether the job can no longer change.
func (s JobStatus) Terminal() bool {
	return s == JobDone || s == JobFailed || s == JobCanceled
}

// Job is a detached, long-running investigation attached to a case. The job
// process writes evidence through the same redacted stamp path as a
// foreground call; the record here is bookkeeping so status can report
// progress, and a dead process is reported as failed rather than "running".
type Job struct {
	ID       string    `json:"id"`
	TaskID   string    `json:"taskId"`
	Kind     string    `json:"kind"` // investigate
	Question string    `json:"question"`
	Depth    string    `json:"depth,omitempty"`
	Modules  []string  `json:"modules,omitempty"` // survey fan-out: one bounded round per module
	Status   JobStatus `json:"status"`
	PID      int       `json:"pid,omitempty"`
	// Progress counts completed rounds so a long fan-out is observable.
	RoundsDone  int        `json:"roundsDone,omitempty"`
	RoundsTotal int        `json:"roundsTotal,omitempty"`
	EvidenceIDs []string   `json:"evidenceIds,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	Error       string     `json:"error,omitempty"`
	LogRef      string     `json:"logRef,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	HeartbeatAt *time.Time `json:"heartbeatAt,omitempty"`
}

// Validate enforces the job invariants.
func (j Job) Validate() error {
	if j.ID == "" {
		return errValidation("job has no id")
	}
	if j.TaskID == "" {
		return errValidation("job has no owning task")
	}
	if j.Kind != "investigate" {
		return errValidation("job kind must be investigate")
	}
	if j.Question == "" && len(j.Modules) == 0 {
		return errValidation("job needs a question or a module fan-out")
	}
	switch j.Status {
	case JobQueued, JobRunning, JobDone, JobFailed, JobCanceled:
	default:
		return errValidation("job status is unknown")
	}
	if j.CreatedAt.IsZero() {
		return errValidation("job has no creation timestamp")
	}
	return nil
}
