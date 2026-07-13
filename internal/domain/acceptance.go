package domain

import (
	"errors"
	"fmt"
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
}

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
