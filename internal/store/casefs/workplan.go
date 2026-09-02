package casefs

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// maxWorkItems bounds one campaign's plan.
const maxWorkItems = 256

func (s *Store) workplanPath(taskID string) string {
	return filepath.Join(s.dir(taskID), "workplan.json")
}

// LoadWorkplan reads a parent case's work plan; an absent plan is empty, not
// an error, so status can render a campaign before any item exists.
func (s *Store) LoadWorkplan(taskID string) (domain.Workplan, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return domain.Workplan{}, err
	}
	return s.loadWorkplanUnlocked(taskID)
}

func (s *Store) loadWorkplanUnlocked(taskID string) (domain.Workplan, error) {
	plan := domain.Workplan{SchemaVersion: 1}
	if err := readJSON(s.workplanPath(taskID), &plan); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.Workplan{SchemaVersion: 1}, nil
		}
		return domain.Workplan{}, err
	}
	return plan, nil
}

// UpdateWorkplan mutates the plan under the task lock and validates the
// result (unique ids, known dependencies, no cycles) before writing.
func (s *Store) UpdateWorkplan(taskID string, mutate func(*domain.Workplan) error) (domain.Workplan, error) {
	var out domain.Workplan
	err := s.withTaskLock(taskID, func() error {
		plan, err := s.loadWorkplanUnlocked(taskID)
		if err != nil {
			return err
		}
		if err := mutate(&plan); err != nil {
			return err
		}
		if len(plan.Items) > maxWorkItems {
			return errors.New("work plan exceeds the item limit")
		}
		if err := plan.Validate(); err != nil {
			return err
		}
		plan.SchemaVersion = 1
		out = plan
		return writeJSON(s.workplanPath(taskID), plan)
	})
	return out, err
}
