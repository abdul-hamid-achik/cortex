package casefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// maxJobsPerTask bounds the detached-job history one case keeps.
const maxJobsPerTask = 128

// ErrJobNotFound is returned when a job id is unknown to the task.
var ErrJobNotFound = errors.New("job not found")

func (s *Store) jobsPath(taskID string) string {
	return filepath.Join(s.dir(taskID), "jobs.json")
}

// JobLogPath is where a detached job process writes its bounded stderr/stdout
// log. It lives inside the case directory so it is owner-only and auditable.
func (s *Store) JobLogPath(taskID, jobID string) (string, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return "", err
	}
	if err := validateStorageName(jobID, "job id"); err != nil {
		return "", err
	}
	dir := filepath.Join(s.dir(taskID), "jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, safeName(jobID)+".log"), nil
}

// AppendJob records a new detached job for a task.
func (s *Store) AppendJob(taskID string, job domain.Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	if job.TaskID != taskID {
		return fmt.Errorf("job %s belongs to %s, not %s", job.ID, job.TaskID, taskID)
	}
	return s.withTaskLock(taskID, func() error {
		jobs, err := s.jobsUnlocked(taskID)
		if err != nil {
			return err
		}
		if len(jobs) >= maxJobsPerTask {
			return fmt.Errorf("task %s already holds %d jobs (limit)", taskID, maxJobsPerTask)
		}
		for _, existing := range jobs {
			if existing.ID == job.ID {
				return fmt.Errorf("job %s already exists", job.ID)
			}
		}
		jobs = append(jobs, job)
		return writeJSON(s.jobsPath(taskID), jobs)
	})
}

// UpdateJob applies a mutation to one job under the task lock.
func (s *Store) UpdateJob(taskID, jobID string, mutate func(*domain.Job) error) (domain.Job, error) {
	var out domain.Job
	err := s.withTaskLock(taskID, func() error {
		jobs, err := s.jobsUnlocked(taskID)
		if err != nil {
			return err
		}
		for i := range jobs {
			if jobs[i].ID != jobID {
				continue
			}
			if err := mutate(&jobs[i]); err != nil {
				return err
			}
			if err := jobs[i].Validate(); err != nil {
				return err
			}
			out = jobs[i]
			return writeJSON(s.jobsPath(taskID), jobs)
		}
		return fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
	})
	return out, err
}

// Jobs reads a task's detached jobs in creation order.
func (s *Store) Jobs(taskID string) ([]domain.Job, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	return s.jobsUnlocked(taskID)
}

func (s *Store) jobsUnlocked(taskID string) ([]domain.Job, error) {
	var jobs []domain.Job
	if err := readJSON(s.jobsPath(taskID), &jobs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return jobs, nil
}
