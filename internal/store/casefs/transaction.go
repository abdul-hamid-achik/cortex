package casefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// HypothesesUpdate runs under the task's cross-process lock after the expected
// case revision has been checked. It may return one evidence record to commit
// with the new hypotheses snapshot (Resolve uses this for provenance).
type HypothesesUpdate func(current []domain.Hypothesis) (next []domain.Hypothesis, evidence *domain.Evidence, err error)

// RawRecord is one already-redacted, size-bounded tool blob staged with an
// atomic case transaction. ID is the raw_<token> segment used by case:// refs.
type RawRecord struct {
	ID      string
	Content string
}

// CommitVerificationBundle publishes the case phase, verifier evidence, raw
// blobs, and receipt batch under one case-revision CAS and one recoverable
// filesystem transaction. A losing verifier run therefore leaves no facts or
// raw output behind for a newer plan/owner to accidentally consume.
func (s *Store) CommitVerificationBundle(c *domain.CaseFile, evidence []domain.Evidence, receipts []domain.VerificationRecord, raws []RawRecord) error {
	if c == nil || c.ID == "" {
		return errors.New("case has no id")
	}
	if c.Status != domain.PhaseVerifying {
		return errors.New("verification commit case must be in verifying phase")
	}
	if err := validateVerificationBatch(receipts); err != nil {
		return err
	}
	evidenceIDs := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		if err := item.Validate(); err != nil {
			return err
		}
		if item.ID == "" || evidenceIDs[item.ID] {
			return fmt.Errorf("evidence id %s is empty or duplicated", item.ID)
		}
		evidenceIDs[item.ID] = true
	}
	rawIDs := make(map[string]bool, len(raws))
	for _, raw := range raws {
		if err := validateStorageName(raw.ID, "raw id"); err != nil {
			return err
		}
		if rawIDs[raw.ID] {
			return fmt.Errorf("raw id %s is duplicated", raw.ID)
		}
		if len(raw.Content) > maxRawFileBytes {
			return fmt.Errorf("raw output %s exceeds %d byte limit", raw.ID, maxRawFileBytes)
		}
		rawIDs[raw.ID] = true
	}

	return s.withTaskLock(c.ID, func() error {
		_, actual, err := s.currentCaseForUpdateUnlocked(c)
		if err != nil {
			return err
		}
		next := nextCaseSnapshot(c, actual)
		values := make([]transactionValue, 0, len(raws)+3)
		for _, raw := range raws {
			name := filepath.Join("raw", raw.ID+".txt")
			if _, err := os.Stat(filepath.Join(s.dir(c.ID), name)); err == nil {
				return fmt.Errorf("raw id %s already exists", raw.ID)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			values = append(values, transactionValue{name: name, bytes: []byte(raw.Content)})
		}
		if len(evidence) > 0 {
			ledger, err := s.evidenceLedgerWithManyUnlocked(c.ID, evidence)
			if err != nil {
				return err
			}
			values = append(values, transactionValue{name: "evidence.jsonl", bytes: ledger})
		}
		existing, err := s.verificationsUnlocked(c.ID)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(existing)+len(receipts))
		for _, record := range existing {
			seen[record.ID] = true
		}
		for _, record := range receipts {
			if seen[record.ID] {
				return fmt.Errorf("verification id %s already exists", record.ID)
			}
			seen[record.ID] = true
		}
		existing = append(existing, receipts...)
		values = append(values,
			transactionValue{name: "verification.json", value: existing},
			transactionValue{name: "case.json", value: &next}, // final commit anchor
		)
		files, err := s.stageTransactionFiles(c.ID, next.Revision, values)
		if err != nil {
			return err
		}
		if err := s.commitStagedFiles(c.ID, next.Revision, files); err != nil {
			return err
		}
		adoptCaseSnapshot(c, &next)
		return nil
	})
}

// CommitPlan preserves the historical plan-only API while delegating to the
// richer atomic bundle used when planning also produces repository-contract
// evidence and raw provenance.
func (s *Store) CommitPlan(c *domain.CaseFile, plan domain.Plan, hypotheses []domain.Hypothesis) error {
	return s.CommitPlanBundle(c, plan, hypotheses, nil, nil)
}

// CommitPlanBundle atomically arbitrates a plan against c.Revision and
// publishes a mutually consistent case.json, plan.json, hypotheses.json,
// evidence ledger, and raw provenance set. Companion files are staged before
// any target is replaced; case.json is the final commit anchor. Evidence and
// raw IDs are immutable and write-once: an exact retry is a no-op, while the
// same ID with different content is rejected. A stale exact retry of an
// already-published bundle adopts the durable revision; a losing distinct
// writer returns RevisionConflictError without touching any companion state.
func (s *Store) CommitPlanBundle(c *domain.CaseFile, plan domain.Plan, hypotheses []domain.Hypothesis, evidence []domain.Evidence, raws []RawRecord) error {
	return s.CommitPlanBundleContext(context.Background(), c, plan, hypotheses, evidence, raws)
}

// CommitPlanBundleContext is CommitPlanBundle with cancellation honored until
// the atomic filesystem publication begins. Once commitStagedFiles starts, it
// completes or rolls back as one non-cancellable unit so cancellation cannot
// expose a half-published case.
func (s *Store) CommitPlanBundleContext(ctx context.Context, c *domain.CaseFile, plan domain.Plan, hypotheses []domain.Hypothesis, evidence []domain.Evidence, raws []RawRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.ID == "" {
		return errors.New("case has no id")
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if err := validateHypothesesSnapshot(hypotheses); err != nil {
		return err
	}
	if !reflect.DeepEqual(plan.Hypotheses, hypotheses) {
		return errors.New("plan hypotheses do not match hypotheses snapshot")
	}
	if !reflect.DeepEqual(c.ChangeBoundary, plan.ChangeBoundary) {
		return errors.New("case boundary does not match plan boundary")
	}
	if !reflect.DeepEqual(c.VerificationRequired, plan.VerificationRequired) {
		return errors.New("case verification requirements do not match plan")
	}
	if c.Status != domain.PhasePlanned {
		return errors.New("committed plan case must be in planned phase")
	}
	evidenceIDs := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		if err := item.Validate(); err != nil {
			return err
		}
		if item.ID == "" || evidenceIDs[item.ID] {
			return fmt.Errorf("evidence id %s is empty or duplicated", item.ID)
		}
		evidenceIDs[item.ID] = true
	}
	rawIDs := make(map[string]bool, len(raws))
	for _, raw := range raws {
		if err := validateStorageName(raw.ID, "raw id"); err != nil {
			return err
		}
		if rawIDs[raw.ID] {
			return fmt.Errorf("raw id %s is duplicated", raw.ID)
		}
		if len(raw.Content) > maxRawFileBytes {
			return fmt.Errorf("raw output %s exceeds %d byte limit", raw.ID, maxRawFileBytes)
		}
		rawIDs[raw.ID] = true
	}

	entered := false
	err := s.withTaskLock(c.ID, func() error {
		entered = true
		if err := ctx.Err(); err != nil {
			return err
		}
		current, actual, err := s.currentCaseForUpdateUnlocked(c)
		if err != nil {
			if errors.Is(err, ErrRevisionConflict) {
				committed, checkErr := s.planBundleMatchesUnlocked(current, c, plan, hypotheses, evidence, raws)
				if checkErr != nil {
					return checkErr
				}
				if committed {
					if err := ctx.Err(); err != nil {
						return err
					}
					adoptCaseSnapshot(c, &current)
					return nil
				}
			}
			return err
		}
		committed, err := s.planBundleMatchesUnlocked(current, c, plan, hypotheses, evidence, raws)
		if err != nil {
			return err
		}
		if committed {
			if err := ctx.Err(); err != nil {
				return err
			}
			adoptCaseSnapshot(c, &current)
			return nil
		}
		next := nextCaseSnapshot(c, actual)
		values := make([]transactionValue, 0, len(raws)+4)
		rawValues, err := s.planRawValuesUnlocked(c.ID, raws)
		if err != nil {
			return err
		}
		values = append(values, rawValues...)
		if len(evidence) > 0 {
			ledger, changed, err := s.planEvidenceLedgerUnlocked(c.ID, evidence)
			if err != nil {
				return err
			}
			if changed {
				values = append(values, transactionValue{name: "evidence.jsonl", bytes: ledger})
			}
		}
		values = append(values,
			transactionValue{name: "plan.json", value: plan},
			transactionValue{name: "hypotheses.json", value: hypotheses},
			transactionValue{name: "case.json", value: &next}, // final entry is the commit anchor
		)
		files, err := s.stageTransactionFiles(c.ID, next.Revision, values)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			removeStagedFiles(files)
			return err
		}
		if err := s.commitStagedFiles(c.ID, next.Revision, files); err != nil {
			return err
		}
		adoptCaseSnapshot(c, &next)
		return nil
	})
	if err != nil && !entered && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *Store) planBundleMatchesUnlocked(current domain.CaseFile, proposed *domain.CaseFile, plan domain.Plan, hypotheses []domain.Hypothesis, evidence []domain.Evidence, raws []RawRecord) (bool, error) {
	if !equivalentCaseContent(current, *proposed) {
		return false, nil
	}
	durablePlan, err := s.loadPlanUnlocked(current.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if !reflect.DeepEqual(durablePlan, plan) {
		return false, nil
	}
	durableHypotheses, err := s.hypothesesUnlocked(current.ID)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(durableHypotheses, hypotheses) {
		return false, nil
	}
	durableEvidence, err := s.evidenceUnlocked(current.ID)
	if err != nil {
		return false, err
	}
	byEvidenceID := make(map[string][]domain.Evidence, len(durableEvidence))
	for _, item := range durableEvidence {
		byEvidenceID[item.ID] = append(byEvidenceID[item.ID], item)
	}
	for _, item := range evidence {
		matches := byEvidenceID[item.ID]
		if len(matches) == 0 {
			return false, nil
		}
		if len(matches) != 1 {
			return false, fmt.Errorf("evidence id %s appears more than once", item.ID)
		}
		if !reflect.DeepEqual(matches[0], item) {
			return false, fmt.Errorf("evidence id %s already exists with different content", item.ID)
		}
	}
	for _, raw := range raws {
		data, err := readFileLimited(filepath.Join(s.dir(current.ID), "raw", raw.ID+".txt"), maxRawFileBytes)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if string(data) != raw.Content {
			return false, fmt.Errorf("raw id %s already exists with different content", raw.ID)
		}
	}
	return true, nil
}

func equivalentCaseContent(left, right domain.CaseFile) bool {
	left.Revision, right.Revision = 0, 0
	left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func (s *Store) planRawValuesUnlocked(taskID string, raws []RawRecord) ([]transactionValue, error) {
	values := make([]transactionValue, 0, len(raws))
	for _, raw := range raws {
		name := filepath.Join("raw", raw.ID+".txt")
		data, err := readFileLimited(filepath.Join(s.dir(taskID), name), maxRawFileBytes)
		switch {
		case err == nil && string(data) == raw.Content:
			continue
		case err == nil:
			return nil, fmt.Errorf("raw id %s already exists with different content", raw.ID)
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
		values = append(values, transactionValue{name: name, bytes: []byte(raw.Content)})
	}
	return values, nil
}

func (s *Store) planEvidenceLedgerUnlocked(taskID string, evidence []domain.Evidence) ([]byte, bool, error) {
	path := filepath.Join(s.dir(taskID), "evidence.jsonl")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	newByID := make(map[string]domain.Evidence, len(evidence))
	for _, item := range evidence {
		newByID[item.ID] = item
	}
	seen := make(map[string]int, len(evidence))
	existing := make(map[string]domain.Evidence, len(evidence))
	if err := readJSONL(path, func(line []byte) error {
		var item domain.Evidence
		if err := json.Unmarshal(line, &item); err != nil {
			return err
		}
		if _, tracked := newByID[item.ID]; tracked {
			seen[item.ID]++
			existing[item.ID] = item
		}
		return nil
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	changed := false
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	for _, item := range evidence {
		switch seen[item.ID] {
		case 0:
			line, err := json.Marshal(item)
			if err != nil {
				return nil, false, err
			}
			if len(line) > maxLedgerRecordBytes {
				return nil, false, fmt.Errorf("evidence.jsonl record exceeds %d byte limit", maxLedgerRecordBytes)
			}
			data = append(data, line...)
			data = append(data, '\n')
			changed = true
		case 1:
			if !reflect.DeepEqual(existing[item.ID], item) {
				return nil, false, fmt.Errorf("evidence id %s already exists with different content", item.ID)
			}
		default:
			return nil, false, fmt.Errorf("evidence id %s appears more than once", item.ID)
		}
	}
	return data, changed, nil
}

// UpdateHypotheses performs a case-revision-guarded read-modify-write of the
// hypotheses snapshot. Distinct concurrent updates can safely retry from a new
// CaseFile; a stale attempt receives the same typed retryable conflict as Save.
// When update returns evidence, the evidence ledger is staged and committed in
// the same locked transaction, before case.json publishes the new revision.
func (s *Store) UpdateHypotheses(c *domain.CaseFile, update HypothesesUpdate) ([]domain.Hypothesis, *domain.Evidence, error) {
	if c == nil || c.ID == "" {
		return nil, nil, errors.New("case has no id")
	}
	if update == nil {
		return nil, nil, errors.New("hypotheses update is required")
	}
	var committed []domain.Hypothesis
	var committedEvidence *domain.Evidence
	err := s.withTaskLock(c.ID, func() error {
		_, actual, err := s.currentCaseForUpdateUnlocked(c)
		if err != nil {
			return err
		}
		currentHypotheses, err := s.hypothesesUnlocked(c.ID)
		if err != nil {
			return err
		}
		nextHypotheses, evidence, err := update(cloneHypotheses(currentHypotheses))
		if err != nil {
			return err
		}
		if err := validateHypothesesSnapshot(nextHypotheses); err != nil {
			return err
		}

		next := nextCaseSnapshot(c, actual)
		values := []transactionValue{{name: "hypotheses.json", value: nextHypotheses}}
		if evidence != nil {
			if evidence.ID == "" {
				return errors.New("evidence has no id")
			}
			if err := evidence.Validate(); err != nil {
				return err
			}
			ledger, err := s.evidenceLedgerWithUnlocked(c.ID, *evidence)
			if err != nil {
				return err
			}
			values = append(values, transactionValue{name: "evidence.jsonl", bytes: ledger})
		}
		values = append(values, transactionValue{name: "case.json", value: &next}) // commit anchor
		files, err := s.stageTransactionFiles(c.ID, next.Revision, values)
		if err != nil {
			return err
		}
		if err := s.commitStagedFiles(c.ID, next.Revision, files); err != nil {
			return err
		}
		adoptCaseSnapshot(c, &next)
		committed = cloneHypotheses(nextHypotheses)
		if evidence != nil {
			evidenceCopy := *evidence
			committedEvidence = &evidenceCopy
		}
		return nil
	})
	return committed, committedEvidence, err
}

func (s *Store) currentCaseForUpdateUnlocked(expected *domain.CaseFile) (domain.CaseFile, uint64, error) {
	if err := domain.ValidateAcceptanceCriteria(expected.AcceptanceCriteria); err != nil {
		return domain.CaseFile{}, 0, err
	}
	var current domain.CaseFile
	if err := readJSON(filepath.Join(s.dir(expected.ID), "case.json"), &current); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return current, 0, fmt.Errorf("case %s: %w", expected.ID, ErrNotFound)
		}
		return current, 0, err
	}
	actual := effectiveRevision(current.Revision)
	want := effectiveRevision(expected.Revision)
	if want != actual {
		return current, actual, &RevisionConflictError{TaskID: expected.ID, Expected: want, Actual: actual}
	}
	if !reflect.DeepEqual(current.AcceptanceCriteria, expected.AcceptanceCriteria) {
		return current, actual, errors.New("acceptance criteria are immutable after case creation")
	}
	return current, actual, nil
}

func nextCaseSnapshot(c *domain.CaseFile, actual uint64) domain.CaseFile {
	next := *c
	if next.SchemaVersion == 0 {
		next.SchemaVersion = domain.SchemaVersion
	}
	next.Revision = actual + 1
	next.UpdatedAt = time.Now().UTC()
	return next
}

func adoptCaseSnapshot(dst, src *domain.CaseFile) {
	dst.SchemaVersion = src.SchemaVersion
	dst.Revision = src.Revision
	dst.UpdatedAt = src.UpdatedAt
}

func validateHypothesesSnapshot(hypotheses []domain.Hypothesis) error {
	seen := make(map[string]bool, len(hypotheses))
	for _, hypothesis := range hypotheses {
		if hypothesis.ID == "" {
			return errors.New("hypothesis has no id")
		}
		if seen[hypothesis.ID] {
			return fmt.Errorf("hypothesis id %s appears more than once", hypothesis.ID)
		}
		seen[hypothesis.ID] = true
		if err := hypothesis.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func cloneHypotheses(in []domain.Hypothesis) []domain.Hypothesis {
	out := append([]domain.Hypothesis(nil), in...)
	for i := range out {
		out[i].Supports = append([]string(nil), out[i].Supports...)
	}
	return out
}

func (s *Store) evidenceLedgerWithUnlocked(taskID string, evidence domain.Evidence) ([]byte, error) {
	return s.evidenceLedgerWithManyUnlocked(taskID, []domain.Evidence{evidence})
}

func (s *Store) evidenceLedgerWithManyUnlocked(taskID string, evidence []domain.Evidence) ([]byte, error) {
	path := filepath.Join(s.dir(taskID), "evidence.jsonl")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	newIDs := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		if item.ID == "" || newIDs[item.ID] {
			return nil, fmt.Errorf("evidence id %s is empty or duplicated", item.ID)
		}
		newIDs[item.ID] = true
	}
	matches := make(map[string]bool, len(evidence))
	if err := readJSONL(path, func(line []byte) error {
		var existing domain.Evidence
		if err := json.Unmarshal(line, &existing); err != nil {
			return err
		}
		if newIDs[existing.ID] {
			matches[existing.ID] = true
		}
		return nil
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for id := range matches {
		return nil, fmt.Errorf("evidence id %s already exists", id)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	for _, item := range evidence {
		line, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if len(line) > maxLedgerRecordBytes {
			return nil, fmt.Errorf("evidence.jsonl record exceeds %d byte limit", maxLedgerRecordBytes)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	return data, nil
}

type transactionValue struct {
	name  string
	value any
	bytes []byte
}

type stagedFile struct {
	target  string
	stage   string
	old     []byte
	existed bool
}

func removeStagedFiles(files []stagedFile) {
	for _, file := range files {
		_ = os.Remove(file.stage)
		_ = os.Remove(file.stage + ".tmp")
	}
}

const transactionJournalName = ".transaction.json"

type transactionJournal struct {
	Revision uint64                   `json:"revision"`
	Files    []transactionJournalFile `json:"files"`
}

type transactionJournalFile struct {
	Name    string `json:"name"`
	Stage   string `json:"stage"`
	Old     []byte `json:"old,omitempty"`
	Existed bool   `json:"existed"`
}

func (s *Store) stageTransactionFiles(taskID string, revision uint64, values []transactionValue) ([]stagedFile, error) {
	files := make([]stagedFile, 0, len(values))
	for _, item := range values {
		target := filepath.Join(s.dir(taskID), item.name)
		stage := fmt.Sprintf("%s.txn-%d", target, revision)
		old, err := os.ReadFile(target)
		existed := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			removeStagedFiles(files)
			return nil, err
		}
		file := stagedFile{target: target, stage: stage, old: old, existed: existed}
		files = append(files, file)
		if item.bytes != nil {
			err = writeFileAtomic(stage, item.bytes, 0o600)
		} else {
			err = writeJSON(stage, item.value)
		}
		if err != nil {
			removeStagedFiles(files)
			return nil, err
		}
	}
	return files, nil
}

func (s *Store) commitStagedFiles(taskID string, revision uint64, files []stagedFile) error {
	journal := transactionJournal{Revision: revision, Files: make([]transactionJournalFile, 0, len(files))}
	for _, file := range files {
		name, err := filepath.Rel(s.dir(taskID), file.target)
		if err != nil {
			return err
		}
		stage, err := filepath.Rel(s.dir(taskID), file.stage)
		if err != nil {
			return err
		}
		journal.Files = append(journal.Files, transactionJournalFile{
			Name: filepath.ToSlash(name), Stage: filepath.ToSlash(stage),
			Old: append([]byte(nil), file.old...), Existed: file.existed,
		})
	}
	journalPath := filepath.Join(s.dir(taskID), transactionJournalName)
	if err := writeJSON(journalPath, journal); err != nil {
		for _, file := range files {
			_ = os.Remove(file.stage)
			_ = os.Remove(file.stage + ".tmp")
		}
		return fmt.Errorf("prepare transaction journal: %w", err)
	}

	committed := 0
	for i, file := range files {
		if err := os.Rename(file.stage, file.target); err != nil {
			rollbackErr := rollbackStagedFiles(files[:committed])
			for _, pending := range files[i:] {
				_ = os.Remove(pending.stage)
				_ = os.Remove(pending.stage + ".tmp")
			}
			if rollbackErr != nil {
				return fmt.Errorf("commit transaction: %v (rollback: %v)", err, rollbackErr)
			}
			_ = os.Remove(journalPath)
			return fmt.Errorf("commit transaction: %w", err)
		}
		committed++
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove committed transaction journal: %w", err)
	}
	return nil
}

// recoverTransactionUnlocked repairs a transaction interrupted between
// companion-file renames. case.json is the final commit anchor: if it reached
// the journal revision, every earlier companion rename also completed and only
// cleanup remains. Otherwise all targets are rolled back from the journal.
// The caller must hold the task lock.
func (s *Store) recoverTransactionUnlocked(taskID string) error {
	journalPath := filepath.Join(s.dir(taskID), transactionJournalName)
	var journal transactionJournal
	if err := readJSON(journalPath, &journal); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read transaction journal: %w", err)
	}
	if journal.Revision == 0 || len(journal.Files) == 0 {
		return errors.New("invalid transaction journal")
	}
	allowed := map[string]bool{
		"case.json": true, "plan.json": true, "hypotheses.json": true,
		"evidence.jsonl": true, "verification.json": true,
	}
	files := make([]stagedFile, 0, len(journal.Files))
	seen := make(map[string]bool, len(journal.Files))
	for _, entry := range journal.Files {
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		stageName := filepath.Clean(filepath.FromSlash(entry.Stage))
		isRaw := filepath.Dir(name) == "raw" && filepath.Ext(name) == ".txt" && filepath.Base(name) == name[len("raw")+1:]
		if (!allowed[entry.Name] && !isRaw) || seen[entry.Name] || filepath.IsAbs(name) || filepath.IsAbs(stageName) || strings.HasPrefix(name, "..") || strings.HasPrefix(stageName, "..") {
			return errors.New("invalid transaction journal file entry")
		}
		seen[entry.Name] = true
		target := filepath.Join(s.dir(taskID), name)
		wantStage := fmt.Sprintf("%s.txn-%d", target, journal.Revision)
		stage := filepath.Join(s.dir(taskID), stageName)
		if stage != wantStage {
			return errors.New("invalid transaction journal stage entry")
		}
		files = append(files, stagedFile{
			target: target, stage: stage, old: append([]byte(nil), entry.Old...), existed: entry.Existed,
		})
	}

	committed := false
	var current domain.CaseFile
	if err := readJSON(filepath.Join(s.dir(taskID), "case.json"), &current); err == nil {
		committed = effectiveRevision(current.Revision) >= journal.Revision
	}
	if !committed {
		if err := rollbackStagedFiles(files); err != nil {
			return fmt.Errorf("recover transaction rollback: %w", err)
		}
	}
	for _, file := range files {
		_ = os.Remove(file.stage)
		_ = os.Remove(file.stage + ".tmp")
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove recovered transaction journal: %w", err)
	}
	return nil
}

// recoverTransaction checks cheaply for a prepared journal, then serializes
// recovery with any live writer. Public readers call this before exposing a
// snapshot; withTaskLock also runs the unlocked form before every mutation.
func (s *Store) recoverTransaction(taskID string) error {
	journalPath := filepath.Join(s.dir(taskID), transactionJournalName)
	if _, err := os.Stat(journalPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return s.withTaskLockNoRecovery(taskID, func() error {
		return s.recoverTransactionUnlocked(taskID)
	})
}

func rollbackStagedFiles(files []stagedFile) error {
	var first error
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		var err error
		if file.existed {
			err = writeFileAtomic(file.target, file.old, 0o600)
		} else {
			err = os.Remove(file.target)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
