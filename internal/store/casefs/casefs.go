// Package casefs persists case files to the local filesystem as JSON/JSONL
// under .cortex/cases/<taskID>/ by default (SPEC §8.1; overridable). It is the kernel's working memory,
// not a transcript: append-oriented ledgers (evidence, commands) plus
// snapshot documents (case, plan, hypotheses, verification, summary).
//
// v0.1 uses files, not a database (SPEC §24 #1). The layout is intentionally
// human-readable so a case can be inspected or hand-edited.
package casefs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// ErrNotFound is returned when a task or record does not exist.
var ErrNotFound = errors.New("not found")

// Store is a filesystem-backed case-file store rooted at a cases directory.
type Store struct {
	root string     // e.g. <workspace>/.cortex/cases
	mu   sync.Mutex // guards AppendVerification read-modify-write
}

// New opens (creating if needed) a case store under the given cases root.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create cases dir: %w", err)
	}
	return &Store{root: root}, nil
}

// Root returns the cases directory this store manages.
func (s *Store) Root() string { return s.root }

// dir returns the on-disk directory for a task, sanitizing the ID so a
// caller-supplied value (taskId comes straight from MCP/CLI input) can't use
// ".." or a path separator to escape the cases root. Legitimate minted IDs
// (task_<base32>) pass through unchanged.
func (s *Store) dir(taskID string) string { return filepath.Join(s.root, safeName(taskID)) }

// Create writes a new case file and its directory skeleton.
func (s *Store) Create(c *domain.CaseFile) error {
	if c.ID == "" {
		return errors.New("case has no id")
	}
	dir := s.dir(c.ID)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("case %s already exists", c.ID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return s.Save(c)
}

// Save writes (or rewrites) case.json, stamping UpdatedAt.
func (s *Store) Save(c *domain.CaseFile) error {
	c.UpdatedAt = time.Now().UTC()
	if c.SchemaVersion == 0 {
		c.SchemaVersion = domain.SchemaVersion
	}
	return writeJSON(filepath.Join(s.dir(c.ID), "case.json"), c)
}

// Load reads a case file by task ID.
func (s *Store) Load(taskID string) (*domain.CaseFile, error) {
	var c domain.CaseFile
	if err := readJSON(filepath.Join(s.dir(taskID), "case.json"), &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("case %s: %w", taskID, ErrNotFound)
		}
		return nil, err
	}
	return &c, nil
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
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

// AppendEvidence appends one evidence record to the task's evidence.jsonl.
func (s *Store) AppendEvidence(taskID string, ev domain.Evidence) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	return appendJSONL(filepath.Join(s.dir(taskID), "evidence.jsonl"), ev)
}

// Evidence returns every evidence record for a task in append order.
func (s *Store) Evidence(taskID string) ([]domain.Evidence, error) {
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
	return appendJSONL(filepath.Join(s.dir(taskID), "commands.jsonl"), cmd)
}

// Commands returns every audited tool invocation for a task in append order —
// the source for observability metrics (SPEC §18), which until now was a
// write-only log with no reader.
func (s *Store) Commands(taskID string) ([]CommandRecord, error) {
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
// capability and result, never secret contents (SPEC §16.2 #7).
type CommandRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Tool        string    `json:"tool"`
	Operation   string    `json:"operation"`
	ActionClass string    `json:"actionClass,omitempty"` // read_only | local_mutation | external_mutation | secreted_execution
	Status      string    `json:"status"`
	DurationMs  int64     `json:"durationMs,omitempty"`
	Note        string    `json:"note,omitempty"`
}

// SavePlan writes plan.json.
func (s *Store) SavePlan(taskID string, p domain.Plan) error {
	return writeJSON(filepath.Join(s.dir(taskID), "plan.json"), p)
}

// LoadPlan reads plan.json (ErrNotFound if the task was never planned).
func (s *Store) LoadPlan(taskID string) (domain.Plan, error) {
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
	return writeJSON(filepath.Join(s.dir(taskID), "hypotheses.json"), hs)
}

// Hypotheses reads the hypotheses snapshot (nil if none).
func (s *Store) Hypotheses(taskID string) ([]domain.Hypothesis, error) {
	var hs []domain.Hypothesis
	if err := readJSON(filepath.Join(s.dir(taskID), "hypotheses.json"), &hs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return hs, nil
}

// AppendVerification appends a verification receipt to verification.json,
// which is maintained as a JSON array (read-modify-write; receipts are few).
// The store mutex guards the read-modify-write so concurrent receipts for the
// same task can't lose one another (both reading N then writing N+1).
func (s *Store) AppendVerification(taskID string, vr domain.VerificationRecord) error {
	if err := vr.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.Verifications(taskID)
	if err != nil {
		return err
	}
	recs = append(recs, vr)
	return writeJSON(filepath.Join(s.dir(taskID), "verification.json"), recs)
}

// Verifications reads all verification receipts (nil if none).
func (s *Store) Verifications(taskID string) ([]domain.VerificationRecord, error) {
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
	return writeFileAtomic(filepath.Join(s.dir(taskID), "summary.md"), []byte(md), 0o644)
}

// WriteRaw persists a tool call's (redacted) raw output under raw/<rawID>.txt so
// it can be retrieved on demand without bloating the model-visible envelope
// (SPEC §10.4). rawID must be a filename-safe token.
func (s *Store) WriteRaw(taskID, rawID, content string) error {
	dir := filepath.Join(s.dir(taskID), "raw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, safeName(rawID)+".txt"), []byte(content), 0o644)
}

// ReadRaw returns a stored raw blob by ID (ErrNotFound if absent).
func (s *Store) ReadRaw(taskID, rawID string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(taskID), "raw", safeName(rawID)+".txt"))
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

// ---- low-level helpers ----

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write to a temp file then rename for atomicity — a crash mid-write must
	// not leave a truncated case.json that fails to parse on the next open.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeFileAtomic writes bytes to path via a temp file + rename, so a crash
// mid-write can't leave a truncated file (matches writeJSON's guarantee for
// non-JSON files like summary.md and raw blobs).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func appendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

func readJSONL(path string, fn func(line []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
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
