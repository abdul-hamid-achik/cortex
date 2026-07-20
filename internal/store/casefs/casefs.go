// Package casefs persists case files to the local filesystem as JSON/JSONL
// under .cortex/cases/<taskID>/ by default (overridable). It is the kernel's working memory,
// not a transcript: append-oriented ledgers (evidence, commands, phases) plus
// snapshot documents (case, plan, hypotheses, verification, summary).
//
// v0.1 uses files, not a database. The layout is intentionally
// human-readable so a case can be inspected or hand-edited.
package casefs

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

var (
	// ErrNotFound is returned when a task or record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrBusy means another Cortex process is currently updating the same task.
	// Callers may safely retry the operation; no partial write was performed.
	ErrBusy = errors.New("case is busy")
	// ErrRevisionConflict identifies a stale case snapshot. Callers should load
	// the latest snapshot, reapply their bounded update, and retry.
	ErrRevisionConflict = errors.New("case revision conflict")
	// ErrInvalidTaskID means an externally supplied task identifier is not in the
	// canonical filename-safe task_<token> form. Invalid IDs are rejected rather
	// than cleaned because cleaning makes distinct inputs alias the same case.
	ErrInvalidTaskID = errors.New("invalid task id")
)

// RevisionConflictError reports the expected and actual snapshot revisions.
// It is typed so transports can classify the failure without parsing text.
type RevisionConflictError struct {
	TaskID   string
	Expected uint64
	Actual   uint64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("case %s revision conflict: expected %d, found %d", e.TaskID, e.Expected, e.Actual)
}

// Unwrap supports errors.Is(err, ErrRevisionConflict).
func (e *RevisionConflictError) Unwrap() error { return ErrRevisionConflict }

// Retryable reports that reloading and reapplying the update is safe.
func (e *RevisionConflictError) Retryable() bool { return true }

// Store is a filesystem-backed case-file store rooted at a cases directory.
type Store struct {
	root string     // e.g. <workspace>/.cortex/cases
	mu   sync.Mutex // guards AppendVerification read-modify-write
}

// New opens (creating if needed) a case store under the given cases root.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create cases dir: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil { // #nosec G302 -- a directory needs owner-execute (0700); this is not a regular file
		return nil, fmt.Errorf("secure cases dir: %w", err)
	}
	return &Store{root: root}, nil
}

// Root returns the cases directory this store manages.
func (s *Store) Root() string { return s.root }

// WithCoordinationLock serializes a bounded cross-task operation across Store
// instances and processes. The caller-supplied identity is hashed before it
// reaches the filesystem so goals or idempotency keys cannot leak through a
// lock filename. Task-local writes inside fn use their own distinct locks.
func (s *Store) WithCoordinationLock(identity string, fn func() error) error {
	if identity == "" {
		return errors.New("coordination lock needs an identity")
	}
	sum := sha256.Sum256([]byte(identity))
	return s.withTaskLock(fmt.Sprintf("coord_%x", sum[:16]), fn)
}

// dir returns the on-disk directory for a task. Every externally reachable
// caller validates first; safeName remains a final path-containment defense.
func (s *Store) dir(taskID string) string { return filepath.Join(s.root, safeName(taskID)) }

// TaskDir returns the on-disk directory for a task (sanitized).
func (s *Store) TaskDir(taskID string) (string, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return "", err
	}
	return s.dir(taskID), nil
}

// RemoveTask permanently deletes a task's directory and everything under it.
// Destructive and irreversible; the caller must gate it behind explicit intent.
func (s *Store) RemoveTask(taskID string) error {
	if err := ValidateTaskID(taskID); err != nil {
		return err
	}
	return os.RemoveAll(s.dir(taskID))
}

// Create writes a new case file and its directory skeleton.
func (s *Store) Create(c *domain.CaseFile) error {
	if c == nil {
		return errors.New("case is nil")
	}
	if err := ValidateTaskID(c.ID); err != nil {
		return err
	}
	if err := domain.ValidateAcceptanceCriteria(c.AcceptanceCriteria); err != nil {
		return err
	}
	return s.withTaskLock(c.ID, func() error {
		dir := s.dir(c.ID)
		if _, err := os.Stat(dir); err == nil {
			return fmt.Errorf("case %s already exists", c.ID)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		c.Revision = 1
		return s.writeCaseUnlocked(c)
	})
}

// Save compare-and-swaps case.json using c.Revision, then advances the caller's
// snapshot to the newly persisted revision. Existing callers do not need a
// separate expected-revision argument: a CaseFile returned by Create or Load
// already carries it.
func (s *Store) Save(c *domain.CaseFile) error {
	if c == nil {
		return errors.New("case is nil")
	}
	if err := ValidateTaskID(c.ID); err != nil {
		return err
	}
	if err := domain.ValidateAcceptanceCriteria(c.AcceptanceCriteria); err != nil {
		return err
	}
	return s.withTaskLock(c.ID, func() error {
		var current domain.CaseFile
		if err := readJSON(filepath.Join(s.dir(c.ID), "case.json"), &current); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("case %s: %w", c.ID, ErrNotFound)
			}
			return err
		}
		actual := effectiveRevision(current.Revision)
		expected := effectiveRevision(c.Revision)
		if expected != actual {
			return &RevisionConflictError{TaskID: c.ID, Expected: expected, Actual: actual}
		}
		if !slices.Equal(current.AcceptanceCriteria, c.AcceptanceCriteria) {
			return errors.New("acceptance criteria are immutable after case creation")
		}

		next := *c
		next.Revision = actual + 1
		if err := s.writeCaseUnlocked(&next); err != nil {
			return err
		}
		c.SchemaVersion = next.SchemaVersion
		c.Revision = next.Revision
		c.UpdatedAt = next.UpdatedAt
		return nil
	})
}

func (s *Store) writeCaseUnlocked(c *domain.CaseFile) error {
	c.UpdatedAt = time.Now().UTC()
	if c.SchemaVersion == 0 {
		c.SchemaVersion = domain.SchemaVersion
	}
	return writeJSON(filepath.Join(s.dir(c.ID), "case.json"), c)
}

// Load reads a case file by task ID.
func (s *Store) Load(taskID string) (*domain.CaseFile, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	if err := s.recoverTransaction(taskID); err != nil {
		return nil, err
	}
	var c domain.CaseFile
	if err := readJSON(filepath.Join(s.dir(taskID), "case.json"), &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("case %s: %w", taskID, ErrNotFound)
		}
		return nil, err
	}
	if c.ID != taskID {
		return nil, fmt.Errorf("case directory %s contains mismatched id %q", taskID, c.ID)
	}
	if err := domain.ValidateAcceptanceCriteria(c.AcceptanceCriteria); err != nil {
		return nil, fmt.Errorf("case %s acceptance criteria: %w", taskID, err)
	}
	// Legacy v0.1 snapshots had no revision. Treat their implicit first
	// snapshot as revision one; the next Save materializes revision two.
	c.Revision = effectiveRevision(c.Revision)
	return &c, nil
}

func effectiveRevision(revision uint64) uint64 {
	if revision == 0 {
		return 1
	}
	return revision
}

// List returns all task IDs in the store, newest first (IDs sort lexically by
// creation time, so a reverse sort is chronological).
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && ValidateTaskID(e.Name()) == nil {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

// MoveTaskTo relocates a task's whole directory to destRoot/<taskID> (an atomic
// rename when both live on the same filesystem). Used to archive a session —
// non-destructive, since the data is moved, not deleted. Fails if the task is
// absent or the destination already exists.
func (s *Store) MoveTaskTo(taskID, destRoot string) error {
	return s.withTaskLock(taskID, func() error {
		src := s.dir(taskID)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
			}
			return err
		}
		dst := filepath.Join(destRoot, safeName(taskID))
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("destination %s already exists", dst)
		}
		if err := os.MkdirAll(destRoot, 0o700); err != nil {
			return err
		}
		return os.Rename(src, dst)
	})
}

// AppendEvidence appends one evidence record to the task's evidence.jsonl.
func (s *Store) AppendEvidence(taskID string, ev domain.Evidence) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	return s.withTaskLock(taskID, func() error {
		return appendJSONL(filepath.Join(s.dir(taskID), "evidence.jsonl"), ev)
	})
}

// AppendEvidenceOnce appends ev only when its ID is absent, returning the
// durable record and whether this call inserted it. Decision-answer recovery
// uses this so two processes completing the same answer cannot duplicate an
// evidence identity in the append-only ledger.
func (s *Store) AppendEvidenceOnce(taskID string, ev domain.Evidence) (domain.Evidence, bool, error) {
	if err := ev.Validate(); err != nil {
		return domain.Evidence{}, false, err
	}
	var durable domain.Evidence
	inserted := false
	err := s.withTaskLock(taskID, func() error {
		matches := 0
		err := readJSONL(filepath.Join(s.dir(taskID), "evidence.jsonl"), func(line []byte) error {
			var existing domain.Evidence
			if err := json.Unmarshal(line, &existing); err != nil {
				return err
			}
			if existing.ID == ev.ID {
				matches++
				durable = existing
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if matches > 1 {
			return fmt.Errorf("evidence id %s appears more than once", ev.ID)
		}
		if matches == 1 {
			if err := durable.Validate(); err != nil {
				return fmt.Errorf("existing evidence %s is invalid: %w", ev.ID, err)
			}
			return nil
		}
		if err := appendJSONL(filepath.Join(s.dir(taskID), "evidence.jsonl"), ev); err != nil {
			return err
		}
		durable = ev
		inserted = true
		return nil
	})
	return durable, inserted, err
}

// Evidence returns every evidence record for a task in append order.
func (s *Store) Evidence(taskID string) ([]domain.Evidence, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var out []domain.Evidence
	err := s.withTaskLock(taskID, func() error {
		var err error
		out, err = s.evidenceUnlocked(taskID)
		return err
	})
	return out, err
}

func (s *Store) evidenceUnlocked(taskID string) ([]domain.Evidence, error) {
	var out []domain.Evidence
	err := readJSONL(filepath.Join(s.dir(taskID), "evidence.jsonl"), func(line []byte) error {
		var ev domain.Evidence
		if err := json.Unmarshal(line, &ev); err != nil {
			return err
		}
		out = append(out, ev)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// GetEvidence returns a single evidence record by ID.
func (s *Store) GetEvidence(taskID, evID string) (domain.Evidence, error) {
	all, err := s.Evidence(taskID)
	if err != nil {
		return domain.Evidence{}, err
	}
	for _, e := range all {
		if e.ID == evID {
			return e, nil
		}
	}
	return domain.Evidence{}, fmt.Errorf("evidence %s: %w", evID, ErrNotFound)
}

// AppendCommand records a command invocation to commands.jsonl (audit trail).
func (s *Store) AppendCommand(taskID string, cmd CommandRecord) error {
	return s.withTaskLock(taskID, func() error {
		return appendJSONL(filepath.Join(s.dir(taskID), "commands.jsonl"), cmd)
	})
}

// Commands returns every audited tool invocation for a task in append order —
// the source for observability metrics, which until now was a
// write-only log with no reader.
func (s *Store) Commands(taskID string) ([]CommandRecord, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var out []CommandRecord
	err := readJSONL(filepath.Join(s.dir(taskID), "commands.jsonl"), func(line []byte) error {
		var cmd CommandRecord
		if err := json.Unmarshal(line, &cmd); err != nil {
			return err
		}
		out = append(out, cmd)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// CommandRecord is an audit entry for a downstream tool invocation. It records
// capability and result, never secret contents.
type CommandRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Actor       string    `json:"actor,omitempty"`
	Tool        string    `json:"tool"`
	Operation   string    `json:"operation"`
	ActionClass string    `json:"actionClass,omitempty"` // read_only | local_mutation | external_mutation | secreted_execution
	Status      string    `json:"status"`
	DurationMs  int64     `json:"durationMs,omitempty"`
	Note        string    `json:"note,omitempty"`
}

// PhaseEvent is one phase transition in a case's history (phases.jsonl): the
// durable trail of the reasoning loop and the timeline's phase source. The
// CaseFile keeps only the *current* phase; this ledger is how "when did it enter
// verifying" becomes answerable.
type PhaseEvent struct {
	Timestamp time.Time    `json:"timestamp"`
	From      domain.Phase `json:"from"`
	To        domain.Phase `json:"to"`
}

// AppendPhaseEvent records a phase transition to phases.jsonl.
func (s *Store) AppendPhaseEvent(taskID string, ev PhaseEvent) error {
	return s.withTaskLock(taskID, func() error {
		return appendJSONL(filepath.Join(s.dir(taskID), "phases.jsonl"), ev)
	})
}

// PhaseEvents returns a case's phase-transition history in append order (nil if
// the case predates phase-history recording).
func (s *Store) PhaseEvents(taskID string) ([]PhaseEvent, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var out []PhaseEvent
	err := readJSONL(filepath.Join(s.dir(taskID), "phases.jsonl"), func(line []byte) error {
		var ev PhaseEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return err
		}
		out = append(out, ev)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// SavePlan writes plan.json.
func (s *Store) SavePlan(taskID string, p domain.Plan) error {
	return s.withTaskLock(taskID, func() error {
		return writeJSON(filepath.Join(s.dir(taskID), "plan.json"), p)
	})
}

// LoadPlan reads plan.json (ErrNotFound if the task was never planned).
func (s *Store) LoadPlan(taskID string) (domain.Plan, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return domain.Plan{}, err
	}
	var plan domain.Plan
	err := s.withTaskLock(taskID, func() error {
		var err error
		plan, err = s.loadPlanUnlocked(taskID)
		return err
	})
	return plan, err
}

func (s *Store) loadPlanUnlocked(taskID string) (domain.Plan, error) {
	var p domain.Plan
	if err := readJSON(filepath.Join(s.dir(taskID), "plan.json"), &p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, fmt.Errorf("plan for %s: %w", taskID, ErrNotFound)
		}
		return p, err
	}
	return p, nil
}

// SaveHypotheses writes the hypotheses snapshot.
func (s *Store) SaveHypotheses(taskID string, hs []domain.Hypothesis) error {
	return s.withTaskLock(taskID, func() error {
		return writeJSON(filepath.Join(s.dir(taskID), "hypotheses.json"), hs)
	})
}

// Hypotheses reads the hypotheses snapshot (nil if none).
func (s *Store) Hypotheses(taskID string) ([]domain.Hypothesis, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var hypotheses []domain.Hypothesis
	err := s.withTaskLock(taskID, func() error {
		var err error
		hypotheses, err = s.hypothesesUnlocked(taskID)
		return err
	})
	return hypotheses, err
}

func (s *Store) hypothesesUnlocked(taskID string) ([]domain.Hypothesis, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var hs []domain.Hypothesis
	if err := readJSON(filepath.Join(s.dir(taskID), "hypotheses.json"), &hs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return hs, nil
}

// AppendDecision adds a decision request to decisions.json. The read-modify-write
// is protected by the cross-instance task lock because MCP constructs a fresh
// Store per call; an in-process mutex alone cannot prevent lost requests.
func (s *Store) AppendDecision(taskID string, decision domain.Decision) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if decision.Status != domain.DecisionPending {
		return fmt.Errorf("new decision %s must be pending", decision.ID)
	}
	return s.withTaskLock(taskID, func() error {
		decisions, err := s.decisionsUnlocked(taskID)
		if err != nil {
			return err
		}
		for _, existing := range decisions {
			if existing.ID == decision.ID {
				return fmt.Errorf("decision %s already exists", decision.ID)
			}
			if existing.Status == domain.DecisionPending {
				return fmt.Errorf("decision %s is already pending", existing.ID)
			}
		}
		decisions = append(decisions, decision)
		return writeJSON(filepath.Join(s.dir(taskID), "decisions.json"), decisions)
	})
}

// Decisions reads the task's decision history in request order.
func (s *Store) Decisions(taskID string) ([]domain.Decision, error) {
	return s.decisionsUnlocked(taskID)
}

func (s *Store) decisionsUnlocked(taskID string) ([]domain.Decision, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var decisions []domain.Decision
	if err := readJSON(filepath.Join(s.dir(taskID), "decisions.json"), &decisions); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return decisions, nil
}

// Decision returns one decision by ID.
func (s *Store) Decision(taskID, decisionID string) (domain.Decision, error) {
	decisions, err := s.Decisions(taskID)
	if err != nil {
		return domain.Decision{}, err
	}
	for _, decision := range decisions {
		if decision.ID == decisionID {
			return decision, nil
		}
	}
	return domain.Decision{}, fmt.Errorf("decision %s: %w", decisionID, ErrNotFound)
}

// AnswerDecision atomically changes one pending decision to answered. The
// pending check occurs inside withTaskLock, so two Store instances racing to
// answer cannot both succeed or overwrite each other's updates.
func (s *Store) AnswerDecision(taskID, decisionID, answer, responder, evidenceID string, answeredAt time.Time, sensitive bool) (domain.Decision, error) {
	var answered domain.Decision
	err := s.withTaskLock(taskID, func() error {
		decisions, err := s.decisionsUnlocked(taskID)
		if err != nil {
			return err
		}
		idx := -1
		for i := range decisions {
			if decisions[i].ID == decisionID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("decision %s: %w", decisionID, ErrNotFound)
		}
		if err := decisions[idx].RecordAnswer(answer, responder, evidenceID, answeredAt, sensitive); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(s.dir(taskID), "decisions.json"), decisions); err != nil {
			return err
		}
		answered = decisions[idx]
		return nil
	})
	return answered, err
}

// AppendVerification appends a verification receipt to verification.json,
// which is maintained as a JSON array (read-modify-write; receipts are few).
// The store mutex guards the read-modify-write so concurrent receipts for the
// same task can't lose one another (both reading N then writing N+1).
func (s *Store) AppendVerification(taskID string, vr domain.VerificationRecord) error {
	return s.AppendVerificationBatch(taskID, []domain.VerificationRecord{vr})
}

// AppendVerificationBatch commits one verifier run atomically. Readers either
// see the entire batch or none of it, so a crash cannot expose a partial set of
// claims that canonical assessment mistakes for a complete run.
func (s *Store) AppendVerificationBatch(taskID string, batch []domain.VerificationRecord) error {
	if err := validateVerificationBatch(batch); err != nil {
		return err
	}
	seenIDs := make(map[string]bool, len(batch))
	for _, vr := range batch {
		seenIDs[vr.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withTaskLock(taskID, func() error {
		recs, err := s.verificationsUnlocked(taskID)
		if err != nil {
			return err
		}
		for _, existing := range recs {
			if seenIDs[existing.ID] {
				return fmt.Errorf("verification id %s already exists", existing.ID)
			}
		}
		recs = append(recs, batch...)
		return writeJSON(filepath.Join(s.dir(taskID), "verification.json"), recs)
	})
}

func validateVerificationBatch(batch []domain.VerificationRecord) error {
	if len(batch) == 0 {
		return errors.New("verification batch is empty")
	}
	batchID := batch[0].BatchID
	if len(batch) > 1 && batchID == "" {
		return errors.New("multi-record verification batch needs a batch id")
	}
	seenIDs := make(map[string]bool, len(batch))
	for _, vr := range batch {
		if err := vr.Validate(); err != nil {
			return err
		}
		if vr.BatchID != batchID {
			return errors.New("verification batch contains mixed batch ids")
		}
		if seenIDs[vr.ID] {
			return fmt.Errorf("verification id %s is duplicated in batch", vr.ID)
		}
		seenIDs[vr.ID] = true
	}
	return nil
}

// Verifications reads all verification receipts (nil if none).
func (s *Store) Verifications(taskID string) ([]domain.VerificationRecord, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var records []domain.VerificationRecord
	err := s.withTaskLock(taskID, func() error {
		var err error
		records, err = s.verificationsUnlocked(taskID)
		return err
	})
	return records, err
}

func (s *Store) verificationsUnlocked(taskID string) ([]domain.VerificationRecord, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	var recs []domain.VerificationRecord
	if err := readJSON(filepath.Join(s.dir(taskID), "verification.json"), &recs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return recs, nil
}

// WriteSummary writes summary.md (the human-readable outcome) atomically, so a
// crash mid-write can't leave a truncated summary (matches writeJSON's guarantee).
func (s *Store) WriteSummary(taskID, md string) error {
	if len(md) > maxSummaryFileBytes {
		return fmt.Errorf("summary exceeds %d byte limit", maxSummaryFileBytes)
	}
	return s.withTaskLock(taskID, func() error {
		return writeFileAtomic(filepath.Join(s.dir(taskID), "summary.md"), []byte(md), 0o600)
	})
}

// WriteRaw persists a tool call's (redacted) raw output under raw/<rawID>.txt so
// it can be retrieved on demand without bloating the model-visible envelope.
// Raw identities are write-once: an exact retry succeeds without rewriting,
// while different content under an existing ID is rejected.
func (s *Store) WriteRaw(taskID, rawID, content string) error {
	if len(content) > maxRawFileBytes {
		return fmt.Errorf("raw output exceeds %d byte limit", maxRawFileBytes)
	}
	return s.withTaskLock(taskID, func() error {
		dir := filepath.Join(s.dir(taskID), "raw")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		path := filepath.Join(dir, safeName(rawID)+".txt")
		existing, err := readFileLimited(path, maxRawFileBytes)
		switch {
		case err == nil && string(existing) == content:
			return nil
		case err == nil:
			return fmt.Errorf("raw id %s already exists with different content", rawID)
		case !errors.Is(err, os.ErrNotExist):
			return err
		default:
			return writeFileAtomic(path, []byte(content), 0o600)
		}
	})
}

// ReadRaw returns a stored raw blob by ID (ErrNotFound if absent).
func (s *Store) ReadRaw(taskID, rawID string) (string, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return "", err
	}
	var data []byte
	err := s.withTaskLock(taskID, func() error {
		var err error
		data, err = readFileLimited(filepath.Join(s.dir(taskID), "raw", safeName(rawID)+".txt"), maxRawFileBytes)
		return err
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("raw %s: %w", rawID, ErrNotFound)
		}
		return "", err
	}
	return string(data), nil
}

// safeName strips path separators so a rawID can't escape the raw/ directory.
func safeName(id string) string {
	out := make([]rune, 0, len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "raw"
	}
	return string(out)
}

// ValidateTaskID accepts the canonical shape used by minted and legacy Cortex
// task IDs. Rewriting invalid characters is intentionally forbidden: task/foo
// and task_foo must never address the same case directory.
func ValidateTaskID(taskID string) error {
	if !strings.HasPrefix(taskID, "task_") {
		return fmt.Errorf("%w: must start with task_", ErrInvalidTaskID)
	}
	if err := validateStorageName(taskID, "task id"); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTaskID, err)
	}
	return nil
}

func validateStorageName(value, label string) error {
	if value == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if len(value) > 256 {
		return fmt.Errorf("%s exceeds 256 bytes", label)
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("%s contains noncanonical characters", label)
	}
	return nil
}

var (
	lockWait      = 5 * time.Second
	lockStale     = 2 * time.Minute
	lockHeartbeat = 5 * time.Second
)

// withTaskLock serializes writes across Store instances and OS processes using
// O_EXCL lock-file creation, which is portable across Cortex's Linux, macOS,
// and Windows release targets. The existing mutex only protects one Store
// instance; MCP constructs a kernel per call, so it cannot prevent lost
// read-modify-write updates by itself.
func (s *Store) withTaskLock(taskID string, fn func() error) error {
	return s.withTaskLockNoRecovery(taskID, func() error {
		if strings.HasPrefix(taskID, "task_") {
			if err := s.recoverTransactionUnlocked(taskID); err != nil {
				return err
			}
		}
		return fn()
	})
}

// withTaskLockNoRecovery is the lock primitive used by transaction recovery.
// Ordinary writers use withTaskLock, which repairs an interrupted transaction
// before applying a new mutation; recovery itself must not recursively recover.
func (s *Store) withTaskLockNoRecovery(taskID string, fn func() error) error {
	if strings.HasPrefix(taskID, "coord_") {
		if err := validateStorageName(taskID, "coordination lock id"); err != nil {
			return err
		}
	} else if err := ValidateTaskID(taskID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("create lock identity: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	lockPath := filepath.Join(s.root, "."+safeName(taskID)+".lock")
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- lockPath derives from safeName(taskID)
		if err == nil {
			prefix := fmt.Sprintf("pid=%d\ntoken=%s\nheartbeat=", os.Getpid(), token)
			heartbeatOffset := int64(len(prefix))
			if _, writeErr := f.WriteString(prefix + lockHeartbeatValue(time.Now())); writeErr != nil {
				_ = f.Close()
				_ = os.Remove(lockPath)
				return writeErr
			}
			stop := make(chan struct{})
			stopped := make(chan struct{})
			go maintainLockHeartbeat(f, heartbeatOffset, stop, stopped)
			defer func() {
				close(stop)
				<-stopped
				_ = f.Close()
				removeOwnedLock(lockPath, token)
			}()
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStale {
			// Age alone cannot distinguish a crashed writer from a live process
			// suspended longer than lockStale (for example SIGSTOP or laptop sleep).
			// Never steal from a live PID: the original owner would resume inside its
			// critical section and overlap the replacement owner.
			if lockOwnerAlive(lockPath) {
				if time.Now().After(deadline) {
					return fmt.Errorf("task %s: %w", taskID, ErrBusy)
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			// Atomically move the stale candidate out of the acquisition path.
			// Only one reaper can win this rename, so competing waiters cannot
			// accidentally remove a replacement lock created by a new owner.
			if reapStaleLock(lockPath, token) {
				continue
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("task %s: %w", taskID, ErrBusy)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func lockHeartbeatValue(now time.Time) string {
	return fmt.Sprintf("%030d\n", now.UTC().UnixNano())
}

func maintainLockHeartbeat(f *os.File, offset int64, stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(lockHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			// Writing through the still-open owner handle updates the inode's
			// modification time without following a path that may have been
			// replaced by another process.
			_, _ = f.WriteAt([]byte(lockHeartbeatValue(now)), offset)
		}
	}
}

func removeOwnedLock(lockPath, token string) {
	data, err := os.ReadFile(lockPath) // #nosec G304 -- lockPath is a store-built path from a validated task id
	if err != nil || !strings.Contains(string(data), "\ntoken="+token+"\n") {
		return
	}
	_ = os.Remove(lockPath)
}

func reapStaleLock(lockPath, reaperToken string) bool {
	reapedPath := lockPath + ".reap." + reaperToken
	if err := os.Rename(lockPath, reapedPath); err != nil {
		return false
	}
	info, err := os.Stat(reapedPath)
	if err != nil {
		return true
	}
	if time.Since(info.ModTime()) > lockStale {
		_ = os.Remove(reapedPath)
		return true
	}
	// The owner refreshed between the waiter's initial stat and rename. Restore
	// with an O_EXCL-like hard link so an intervening new owner is never
	// overwritten. If the path is already occupied, leave the live inode alone;
	// its owner still holds the open handle and will clean it up on exit.
	if err := os.Link(reapedPath, lockPath); err == nil {
		_ = os.Remove(reapedPath)
	}
	return false
}

// ---- low-level helpers ----

const (
	maxSnapshotFileBytes     = 16 << 20
	maxTransactionFileBytes  = 64 << 20
	maxLedgerRecordBytes     = 256 << 10
	maxLegacyLedgerLineBytes = 4 << 20
	maxRawFileBytes          = 16 << 20
	maxSummaryFileBytes      = 1 << 20
)

func writeJSON(path string, v any) error {
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	limit := maxSnapshotFileBytes
	if filepath.Base(path) == transactionJournalName {
		limit = maxTransactionFileBytes
	}
	if data.Len() > limit {
		return fmt.Errorf("%s exceeds %d byte snapshot limit", filepath.Base(path), limit)
	}
	return writeFileAtomic(path, data.Bytes(), 0o600)
}

// writeFileAtomic writes bytes to path via a temp file + rename, so a crash
// mid-write can't leave a truncated file (matches writeJSON's guarantee for
// non-JSON files like summary.md and raw blobs).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	limit := maxSnapshotFileBytes
	if filepath.Base(path) == transactionJournalName {
		limit = maxTransactionFileBytes
	}
	data, err := readFileLimited(path, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func readFileLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- path is a store-built path from a validated task id
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d byte limit", filepath.Base(path), limit)
	}
	return data, nil
}

func appendJSONL(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(line) > maxLedgerRecordBytes {
		return fmt.Errorf("%s record exceeds %d byte limit", filepath.Base(path), maxLedgerRecordBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path is a store-built path from a validated task id
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(append(line, '\n'))
	return err
}

func readJSONL(path string, fn func(line []byte) error) error {
	f, err := os.Open(path) // #nosec G304 -- path is a store-built path from a validated task id
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLegacyLedgerLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		if err := fn(cp); err != nil {
			return err
		}
	}
	return sc.Err()
}
