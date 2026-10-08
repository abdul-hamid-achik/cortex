package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	// MaxAcceptanceCriteria and the per-field limits intentionally match the
	// durable goal contract used by local-agent. Keeping one bounded shape lets
	// a harness pass criteria to Cortex without lossy prose encoding.
	MaxAcceptanceCriteria                = 64
	MaxAcceptanceCriterionIDBytes        = 128
	MaxAcceptanceCriterionStatementBytes = 4 << 10
)

// AcceptanceCriterion is one immutable, independently verifiable success
// rule attached when a case is created. VerificationClaim.ID binds a verifier
// result to the criterion; Statement must continue to match exactly.
type AcceptanceCriterion struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	// Kind is empty for an ordinary (behavioral) criterion that only a verifier
	// receipt can prove. CriterionKindProcess marks a process rule (no commit
	// was made, a baseline was recorded) that an evidence-backed agent
	// attestation may satisfy. It is fixed at registration like the statement.
	Kind string `json:"kind,omitempty"`
}

// CriterionKindProcess marks an acceptance criterion about how the work was
// done rather than what the code does.
const CriterionKindProcess = "process"

// Validate enforces the durable, transport-independent criterion bounds.
func (c AcceptanceCriterion) Validate() error {
	if !utf8.ValidString(c.ID) || strings.TrimSpace(c.ID) == "" || c.ID != strings.TrimSpace(c.ID) {
		return errors.New("acceptance criterion id must be non-empty, trimmed UTF-8")
	}
	if len(c.ID) > MaxAcceptanceCriterionIDBytes {
		return fmt.Errorf("acceptance criterion id exceeds %d bytes", MaxAcceptanceCriterionIDBytes)
	}
	if !utf8.ValidString(c.Statement) || strings.TrimSpace(c.Statement) == "" || c.Statement != strings.TrimSpace(c.Statement) {
		return errors.New("acceptance criterion statement must be non-empty, trimmed UTF-8")
	}
	if len(c.Statement) > MaxAcceptanceCriterionStatementBytes {
		return fmt.Errorf("acceptance criterion statement exceeds %d bytes", MaxAcceptanceCriterionStatementBytes)
	}
	if c.Kind != "" && c.Kind != CriterionKindProcess {
		return fmt.Errorf("acceptance criterion kind must be empty or %q", CriterionKindProcess)
	}
	return nil
}

// ValidateAcceptanceCriteria validates an optional immutable criterion set.
func ValidateAcceptanceCriteria(criteria []AcceptanceCriterion) error {
	if len(criteria) > MaxAcceptanceCriteria {
		return fmt.Errorf("acceptance criteria exceed %d entries", MaxAcceptanceCriteria)
	}
	seen := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		if err := criterion.Validate(); err != nil {
			return err
		}
		if _, ok := seen[criterion.ID]; ok {
			return fmt.Errorf("duplicate acceptance criterion id %q", criterion.ID)
		}
		seen[criterion.ID] = struct{}{}
	}
	return nil
}

const (
	// MaxAllowedPaths and MaxAllowedPathBytes bound the owner-registered path
	// contract.
	MaxAllowedPaths     = 64
	MaxAllowedPathBytes = 512
)

// ValidateAllowedPaths checks an owner-registered path contract: clean,
// relative, slash-separated path.Match patterns that never climb out of the
// workspace. An empty contract means "no restriction" (legacy behavior).
func ValidateAllowedPaths(patterns []string) error {
	if len(patterns) > MaxAllowedPaths {
		return fmt.Errorf("allowed paths exceed %d entries", MaxAllowedPaths)
	}
	seen := make(map[string]bool, len(patterns))
	for _, pattern := range patterns {
		if pattern == "" || pattern != strings.TrimSpace(pattern) || !utf8.ValidString(pattern) || len(pattern) > MaxAllowedPathBytes {
			return fmt.Errorf("allowed path %q must be a non-empty, trimmed pattern of at most %d bytes", pattern, MaxAllowedPathBytes)
		}
		if strings.HasPrefix(pattern, "/") || strings.Contains(pattern, `\`) || path.Clean(pattern) != pattern ||
			pattern == ".." || strings.HasPrefix(pattern, "../") {
			return fmt.Errorf("allowed path %q must be a clean workspace-relative pattern", pattern)
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("allowed path %q is not a valid pattern", pattern)
		}
		if seen[pattern] {
			return fmt.Errorf("duplicate allowed path %q", pattern)
		}
		seen[pattern] = true
	}
	return nil
}

// PathAllowed reports whether a workspace-relative file matches the contract.
// An empty contract allows every path.
func PathAllowed(patterns []string, file string) bool {
	if len(patterns) == 0 {
		return true
	}
	file = path.Clean(strings.ReplaceAll(file, `\`, "/"))
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, file); matched {
			return true
		}
	}
	return false
}
