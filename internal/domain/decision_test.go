package domain

import (
	"testing"
	"time"
)

func validDecision() Decision {
	return Decision{
		ID: "dec_1", Question: "Which repair?", Requester: "agent",
		RequestedAt: time.Now().UTC(), Status: DecisionPending,
		Options: []DecisionOption{
			{ID: "small", Label: "Small repair", Consequence: "Lower risk"},
			{ID: "broad", Label: "Broad repair", Consequence: "More coverage"},
		},
	}
}

func TestDecisionValidateAndAnswer(t *testing.T) {
	d := validDecision()
	if err := d.Validate(); err != nil {
		t.Fatalf("valid pending decision rejected: %v", err)
	}
	answeredAt := time.Now().UTC()
	if err := d.RecordAnswer("small", "human", "ev_1", answeredAt, false); err != nil {
		t.Fatalf("valid answer rejected: %v", err)
	}
	if d.Status != DecisionAnswered || d.Answer != "small" || d.AnsweredAt == nil || d.EvidenceID != "ev_1" {
		t.Fatalf("answer fields not recorded: %+v", d)
	}
	if err := d.RecordAnswer("broad", "other", "ev_2", answeredAt, false); err == nil {
		t.Error("an answered decision must not be answered twice")
	}
}

func TestDecisionRejectsMalformedRequestAndAnswer(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Decision)
	}{
		{"no question", func(d *Decision) { d.Question = "" }},
		{"one option", func(d *Decision) { d.Options = d.Options[:1] }},
		{"duplicate option", func(d *Decision) { d.Options[1].ID = d.Options[0].ID }},
		{"no consequence", func(d *Decision) { d.Options[0].Consequence = "" }},
		{"unknown status", func(d *Decision) { d.Status = "maybe" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDecision()
			tt.edit(&d)
			if err := d.Validate(); err == nil {
				t.Fatalf("malformed decision accepted: %+v", d)
			}
		})
	}
	d := validDecision()
	if err := d.RecordAnswer("missing", "human", "ev_1", time.Now().UTC(), false); err == nil {
		t.Error("answer outside the option set should be rejected")
	}
}
