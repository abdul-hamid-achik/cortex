package casefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// maxFindingsPerTask bounds the backlog one case may accumulate so a runaway
// survey cannot turn every later read into an unbounded allocation.
const maxFindingsPerTask = 512

// ErrFindingNotFound is returned when a finding id is unknown to the task.
var ErrFindingNotFound = errors.New("finding not found")

func (s *Store) findingsPath(taskID string) string {
	return filepath.Join(s.dir(taskID), "findings.json")
}

// AppendFinding records a new finding for a task. IDs are unique per task.
func (s *Store) AppendFinding(taskID string, finding domain.Finding) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	if finding.TaskID != taskID {
		return fmt.Errorf("finding %s belongs to %s, not %s", finding.ID, finding.TaskID, taskID)
	}
	return s.withTaskLock(taskID, func() error {
		findings, err := s.findingsUnlocked(taskID)
		if err != nil {
			return err
		}
		if len(findings) >= maxFindingsPerTask {
			return fmt.Errorf("task %s already holds %d findings (limit)", taskID, maxFindingsPerTask)
		}
		for _, existing := range findings {
			if existing.ID == finding.ID {
				return fmt.Errorf("finding %s already exists", finding.ID)
			}
		}
		findings = append(findings, finding)
		return writeJSON(s.findingsPath(taskID), findings)
	})
}

// UpdateFinding applies a mutation to one finding under the task lock and
// re-validates the result before it is written.
func (s *Store) UpdateFinding(taskID, findingID string, mutate func(*domain.Finding) error) (domain.Finding, error) {
	var out domain.Finding
	err := s.withTaskLock(taskID, func() error {
		findings, err := s.findingsUnlocked(taskID)
		if err != nil {
			return err
		}
		for i := range findings {
			if findings[i].ID != findingID {
				continue
			}
			if err := mutate(&findings[i]); err != nil {
				return err
			}
			if err := findings[i].Validate(); err != nil {
				return err
			}
			out = findings[i]
			return writeJSON(s.findingsPath(taskID), findings)
		}
		return fmt.Errorf("%w: %s", ErrFindingNotFound, findingID)
	})
	return out, err
}

// Findings reads a task's findings in creation order.
func (s *Store) Findings(taskID string) ([]domain.Finding, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	return s.findingsUnlocked(taskID)
}

func (s *Store) findingsUnlocked(taskID string) ([]domain.Finding, error) {
	var findings []domain.Finding
	if err := readJSON(s.findingsPath(taskID), &findings); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return findings, nil
}
