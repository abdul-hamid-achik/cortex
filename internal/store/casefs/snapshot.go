package casefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// TaskSnapshot is one revision-consistent read of the documents and ledgers
// used by Status, Show, Studio, timelines, and handoffs. Writers use the same
// cross-process task lock, so readers cannot combine a new case.json with an
// older plan, evidence ledger, decision, or verifier batch.
type TaskSnapshot struct {
	Case          *domain.CaseFile
	Plan          *domain.Plan
	Hypotheses    []domain.Hypothesis
	Evidence      []domain.Evidence
	Verifications []domain.VerificationRecord
	Decisions     []domain.Decision
	PhaseEvents   []PhaseEvent
	Commands      []CommandRecord
	EvidenceTotal int
	// ShareableEvidenceTotal excludes records explicitly marked sensitive.
	// HandoffSnapshot uses it to report omissions without retaining the ledger.
	ShareableEvidenceTotal   int
	SensitiveEvidenceOmitted int
	VerificationTotal        int
	DecisionTotal            int
	PhaseTotal               int
	CommandTotal             int
}

// Snapshot reads a complete task projection under the task's coordination
// lock. It also runs transaction recovery before the first read.
func (s *Store) Snapshot(taskID string) (TaskSnapshot, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return TaskSnapshot{}, err
	}
	var snapshot TaskSnapshot
	err := s.withTaskLock(taskID, func() error {
		c, err := s.Load(taskID)
		if err != nil {
			return err
		}
		snapshot.Case = c
		plan, err := s.loadPlanUnlocked(taskID)
		if err == nil {
			snapshot.Plan = &plan
		} else if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("load plan: %w", err)
		}
		if snapshot.Hypotheses, err = s.hypothesesUnlocked(taskID); err != nil {
			return fmt.Errorf("load hypotheses: %w", err)
		}
		if snapshot.Evidence, err = s.evidenceUnlocked(taskID); err != nil {
			return fmt.Errorf("load evidence: %w", err)
		}
		snapshot.EvidenceTotal = len(snapshot.Evidence)
		for _, item := range snapshot.Evidence {
			if item.Sensitivity == domain.SensitivitySensitive {
				snapshot.SensitiveEvidenceOmitted++
			} else {
				snapshot.ShareableEvidenceTotal++
			}
		}
		if snapshot.Verifications, err = s.verificationsUnlocked(taskID); err != nil {
			return fmt.Errorf("load verifications: %w", err)
		}
		if snapshot.Decisions, err = s.decisionsUnlocked(taskID); err != nil {
			return fmt.Errorf("load decisions: %w", err)
		}
		if snapshot.PhaseEvents, err = s.PhaseEvents(taskID); err != nil {
			return fmt.Errorf("load phases: %w", err)
		}
		if snapshot.Commands, err = s.Commands(taskID); err != nil {
			return fmt.Errorf("load commands: %w", err)
		}
		snapshot.VerificationTotal = len(snapshot.Verifications)
		snapshot.DecisionTotal = len(snapshot.Decisions)
		snapshot.PhaseTotal = len(snapshot.PhaseEvents)
		snapshot.CommandTotal = len(snapshot.Commands)
		return nil
	})
	return snapshot, err
}

// HandoffSnapshot reads the same case/plan/hypothesis/receipt/decision state as
// Snapshot but streams the evidence ledger and retains only the newest
// non-sensitive limit records. It deliberately omits commands and phase events,
// which are not part of a transfer packet. Memory use is therefore independent
// of accumulated evidence count.
func (s *Store) HandoffSnapshot(taskID string, limit int) (TaskSnapshot, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return TaskSnapshot{}, err
	}
	if limit < 0 {
		return TaskSnapshot{}, errors.New("handoff evidence limit cannot be negative")
	}
	var snapshot TaskSnapshot
	err := s.withTaskLock(taskID, func() error {
		c, err := s.Load(taskID)
		if err != nil {
			return err
		}
		snapshot.Case = c
		plan, err := s.loadPlanUnlocked(taskID)
		if err == nil {
			snapshot.Plan = &plan
		} else if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("load plan: %w", err)
		}
		if snapshot.Hypotheses, err = s.hypothesesUnlocked(taskID); err != nil {
			return fmt.Errorf("load hypotheses: %w", err)
		}
		if err := s.readRecentShareableEvidence(taskID, limit, &snapshot); err != nil {
			return fmt.Errorf("load evidence: %w", err)
		}
		if snapshot.Verifications, err = s.verificationsUnlocked(taskID); err != nil {
			return fmt.Errorf("load verifications: %w", err)
		}
		if snapshot.Decisions, err = s.decisionsUnlocked(taskID); err != nil {
			return fmt.Errorf("load decisions: %w", err)
		}
		snapshot.VerificationTotal = len(snapshot.Verifications)
		snapshot.DecisionTotal = len(snapshot.Decisions)
		return nil
	})
	return snapshot, err
}

// CompletionSnapshot reads every document that gates or describes completion
// under one task lock. Evidence is streamed so a long-running task does not
// allocate its full append-only ledger; exact totals are retained while only
// the newest non-sensitive records are returned for summary rendering.
//
// Unlike HandoffSnapshot, this projection intentionally excludes the plan,
// decisions, commands, and phase history. Remember only needs the current case,
// hypothesis ledger, verification receipts, and bounded evidence projection.
func (s *Store) CompletionSnapshot(taskID string, evidenceLimit int) (TaskSnapshot, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return TaskSnapshot{}, err
	}
	if evidenceLimit < 0 {
		return TaskSnapshot{}, errors.New("completion evidence limit cannot be negative")
	}
	var snapshot TaskSnapshot
	err := s.withTaskLock(taskID, func() error {
		c, err := s.Load(taskID)
		if err != nil {
			return err
		}
		snapshot.Case = c
		if snapshot.Hypotheses, err = s.hypothesesUnlocked(taskID); err != nil {
			return fmt.Errorf("load hypotheses: %w", err)
		}
		if err := s.readRecentShareableEvidence(taskID, evidenceLimit, &snapshot); err != nil {
			return fmt.Errorf("load evidence: %w", err)
		}
		if snapshot.Verifications, err = s.verificationsUnlocked(taskID); err != nil {
			return fmt.Errorf("load verifications: %w", err)
		}
		snapshot.VerificationTotal = len(snapshot.Verifications)
		return nil
	})
	return snapshot, err
}

func (s *Store) readRecentShareableEvidence(taskID string, limit int, snapshot *TaskSnapshot) error {
	path := filepath.Join(s.dir(taskID), "evidence.jsonl")
	err := readJSONL(path, func(line []byte) error {
		var item domain.Evidence
		if err := json.Unmarshal(line, &item); err != nil {
			return err
		}
		snapshot.EvidenceTotal++
		if item.Sensitivity == domain.SensitivitySensitive {
			snapshot.SensitiveEvidenceOmitted++
			return nil
		}
		snapshot.ShareableEvidenceTotal++
		if limit == 0 {
			return nil
		}
		if len(snapshot.Evidence) < limit {
			snapshot.Evidence = append(snapshot.Evidence, item)
			return nil
		}
		copy(snapshot.Evidence, snapshot.Evidence[1:])
		snapshot.Evidence[len(snapshot.Evidence)-1] = item
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// StatusSnapshot reads only the documents needed by Status and counts evidence
// while streaming it. Status therefore remains cheap even for a long-running
// case with a large append-only evidence history.
func (s *Store) StatusSnapshot(taskID string) (TaskSnapshot, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return TaskSnapshot{}, err
	}
	var snapshot TaskSnapshot
	err := s.withTaskLock(taskID, func() error {
		c, err := s.Load(taskID)
		if err != nil {
			return err
		}
		snapshot.Case = c
		if snapshot.Hypotheses, err = s.hypothesesUnlocked(taskID); err != nil {
			return fmt.Errorf("load hypotheses: %w", err)
		}
		path := filepath.Join(s.dir(taskID), "evidence.jsonl")
		err = readJSONL(path, func(line []byte) error {
			var item domain.Evidence
			if err := json.Unmarshal(line, &item); err != nil {
				return err
			}
			snapshot.EvidenceTotal++
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load evidence: %w", err)
		}
		if snapshot.Verifications, err = s.verificationsUnlocked(taskID); err != nil {
			return fmt.Errorf("load verifications: %w", err)
		}
		if snapshot.Decisions, err = s.decisionsUnlocked(taskID); err != nil {
			return fmt.Errorf("load decisions: %w", err)
		}
		snapshot.VerificationTotal = len(snapshot.Verifications)
		snapshot.DecisionTotal = len(snapshot.Decisions)
		return nil
	})
	return snapshot, err
}

// ViewSnapshot bounds append-only ledgers for auto-refreshing Show/Studio
// projections while retaining complete current plan, hypotheses, receipts, and
// decisions. Counts expose truncation and direct users to the dedicated
// evidence/timeline commands for older records.
func (s *Store) ViewSnapshot(taskID string, ledgerLimit int) (TaskSnapshot, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return TaskSnapshot{}, err
	}
	if ledgerLimit < 0 {
		return TaskSnapshot{}, errors.New("view ledger limit cannot be negative")
	}
	var snapshot TaskSnapshot
	err := s.withTaskLock(taskID, func() error {
		c, err := s.Load(taskID)
		if err != nil {
			return err
		}
		snapshot.Case = c
		plan, err := s.loadPlanUnlocked(taskID)
		if err == nil {
			snapshot.Plan = &plan
		} else if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("load plan: %w", err)
		}
		if snapshot.Hypotheses, err = s.hypothesesUnlocked(taskID); err != nil {
			return fmt.Errorf("load hypotheses: %w", err)
		}
		if err := readJSONLRing(filepath.Join(s.dir(taskID), "evidence.jsonl"), ledgerLimit, &snapshot.EvidenceTotal, &snapshot.Evidence); err != nil {
			return fmt.Errorf("load evidence: %w", err)
		}
		for _, item := range snapshot.Evidence {
			if item.Sensitivity == domain.SensitivitySensitive {
				snapshot.SensitiveEvidenceOmitted++
			} else {
				snapshot.ShareableEvidenceTotal++
			}
		}
		if snapshot.Verifications, err = s.verificationsUnlocked(taskID); err != nil {
			return fmt.Errorf("load verifications: %w", err)
		}
		if snapshot.Decisions, err = s.decisionsUnlocked(taskID); err != nil {
			return fmt.Errorf("load decisions: %w", err)
		}
		if err := readJSONLRing(filepath.Join(s.dir(taskID), "phases.jsonl"), ledgerLimit, &snapshot.PhaseTotal, &snapshot.PhaseEvents); err != nil {
			return fmt.Errorf("load phases: %w", err)
		}
		if err := readJSONLRing(filepath.Join(s.dir(taskID), "commands.jsonl"), ledgerLimit, &snapshot.CommandTotal, &snapshot.Commands); err != nil {
			return fmt.Errorf("load commands: %w", err)
		}
		snapshot.VerificationTotal = len(snapshot.Verifications)
		snapshot.DecisionTotal = len(snapshot.Decisions)
		return nil
	})
	return snapshot, err
}

func readJSONLRing[T any](path string, limit int, total *int, out *[]T) error {
	err := readJSONL(path, func(line []byte) error {
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			return err
		}
		*total++
		if limit == 0 {
			return nil
		}
		if len(*out) < limit {
			*out = append(*out, item)
			return nil
		}
		copy(*out, (*out)[1:])
		(*out)[len(*out)-1] = item
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
