package domain

import (
	"strings"
	"testing"
)

func TestValidateAcceptanceCriteriaBoundsAndIdentity(t *testing.T) {
	valid := []AcceptanceCriterion{{ID: "criterion_1", Statement: "The tests pass"}}
	if err := ValidateAcceptanceCriteria(valid); err != nil {
		t.Fatalf("valid criteria rejected: %v", err)
	}
	for name, criteria := range map[string][]AcceptanceCriterion{
		"duplicate": {
			{ID: "criterion_1", Statement: "first"},
			{ID: "criterion_1", Statement: "second"},
		},
		"id too long":        {{ID: strings.Repeat("i", MaxAcceptanceCriterionIDBytes+1), Statement: "ok"}},
		"statement too long": {{ID: "criterion_1", Statement: strings.Repeat("s", MaxAcceptanceCriterionStatementBytes+1)}},
		"untrimmed":          {{ID: " criterion_1", Statement: "ok"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAcceptanceCriteria(criteria); err == nil {
				t.Fatalf("invalid criteria accepted: %#v", criteria)
			}
		})
	}
	tooMany := make([]AcceptanceCriterion, MaxAcceptanceCriteria+1)
	for i := range tooMany {
		tooMany[i] = AcceptanceCriterion{ID: "criterion_" + strings.Repeat("x", i+1), Statement: "ok"}
	}
	if err := ValidateAcceptanceCriteria(tooMany); err == nil {
		t.Fatal("too many criteria accepted")
	}
}
