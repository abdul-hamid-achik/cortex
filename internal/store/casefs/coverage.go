package casefs

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// ErrNoCoverage is returned when a task has no coverage ledger (it is not a
// survey, or the ledger was never built).
var ErrNoCoverage = errors.New("no coverage ledger")

func (s *Store) coveragePath(taskID string) string {
	return filepath.Join(s.dir(taskID), "coverage.json")
}

// SaveCoverage writes the survey coverage ledger for a task.
func (s *Store) SaveCoverage(taskID string, ledger domain.CoverageLedger) error {
	if err := ValidateTaskID(taskID); err != nil {
		return err
	}
	return s.withTaskLock(taskID, func() error {
		return writeJSON(s.coveragePath(taskID), ledger)
	})
}

// LoadCoverage reads the coverage ledger; ErrNoCoverage when absent.
func (s *Store) LoadCoverage(taskID string) (domain.CoverageLedger, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return domain.CoverageLedger{}, err
	}
	return s.loadCoverageUnlocked(taskID)
}

func (s *Store) loadCoverageUnlocked(taskID string) (domain.CoverageLedger, error) {
	var ledger domain.CoverageLedger
	if err := readJSON(s.coveragePath(taskID), &ledger); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.CoverageLedger{}, ErrNoCoverage
		}
		return domain.CoverageLedger{}, err
	}
	return ledger, nil
}

// UpdateCoverage mutates the ledger under the task lock. Absent ledgers are
// reported as ErrNoCoverage rather than silently created.
func (s *Store) UpdateCoverage(taskID string, mutate func(*domain.CoverageLedger) error) (domain.CoverageLedger, error) {
	var out domain.CoverageLedger
	err := s.withTaskLock(taskID, func() error {
		ledger, err := s.loadCoverageUnlocked(taskID)
		if err != nil {
			return err
		}
		if err := mutate(&ledger); err != nil {
			return err
		}
		out = ledger
		return writeJSON(s.coveragePath(taskID), ledger)
	})
	return out, err
}
