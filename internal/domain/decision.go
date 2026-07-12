package domain

import (
	"fmt"
	"time"
)

// DecisionStatus tracks whether a requested human decision is still waiting or
// has an answer recorded in the case file.
type DecisionStatus string

const (
	DecisionPending  DecisionStatus = "pending"
	DecisionAnswered DecisionStatus = "answered"
)

// DecisionOption is one answer a human may choose. Consequence explains the
// trade-off before the answer is made, rather than reconstructing it afterward.
type DecisionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

// Decision is the durable request/answer record stored in decisions.json.
// Answer is a DecisionOption.ID; human-readable label and consequence remain on
// the option so the historical record stays self-contained.
type Decision struct {
	ID          string           `json:"id"`
	Question    string           `json:"question"`
	Options     []DecisionOption `json:"options"`
	Requester   string           `json:"requester"`
	RequestedAt time.Time        `json:"requestedAt"`
	Status      DecisionStatus   `json:"status"`
	Answer      string           `json:"answer,omitempty"`
	Responder   string           `json:"responder,omitempty"`
	AnsweredAt  *time.Time       `json:"answeredAt,omitempty"`
	EvidenceID  string           `json:"evidenceId,omitempty"`
	Sensitive   bool             `json:"sensitive,omitempty"`
}

// Validate checks both request and answered shapes. A real decision needs at
// least two distinct choices and an explicit consequence for each.
func (d Decision) Validate() error {
	if d.ID == "" {
		return errValidation("decision has no id")
	}
	if d.Question == "" {
		return errValidation("decision has no question")
	}
	if d.Requester == "" {
		return errValidation("decision has no requester")
	}
	if d.RequestedAt.IsZero() {
		return errValidation("decision has no request timestamp")
	}
	if len(d.Options) < 2 {
		return errValidation("decision needs at least two options")
	}
	seen := make(map[string]bool, len(d.Options))
	for _, option := range d.Options {
		if option.ID == "" || option.Label == "" || option.Consequence == "" {
			return errValidation("each decision option needs an id, label, and consequence")
		}
		if seen[option.ID] {
			return errValidation("decision option ids must be unique")
		}
		seen[option.ID] = true
	}
	switch d.Status {
	case DecisionPending:
		if d.Answer != "" || d.Responder != "" || d.AnsweredAt != nil || d.EvidenceID != "" {
			return errValidation("pending decision has answer fields")
		}
	case DecisionAnswered:
		if !seen[d.Answer] {
			return errValidation("decision answer does not match an option")
		}
		if d.Responder == "" || d.AnsweredAt == nil || d.AnsweredAt.IsZero() {
			return errValidation("answered decision needs responder and timestamp")
		}
		if d.EvidenceID == "" {
			return errValidation("answered decision has no evidence id")
		}
	default:
		return errValidation("decision status must be pending or answered")
	}
	return nil
}

// Option returns the selected option by ID.
func (d Decision) Option(id string) (DecisionOption, bool) {
	for _, option := range d.Options {
		if option.ID == id {
			return option, true
		}
	}
	return DecisionOption{}, false
}

// RecordAnswer validates and records a human answer on a pending decision.
func (d *Decision) RecordAnswer(answer, responder, evidenceID string, at time.Time, sensitive bool) error {
	if d.Status != DecisionPending {
		return fmt.Errorf("decision %s is not pending", d.ID)
	}
	if _, ok := d.Option(answer); !ok {
		return fmt.Errorf("answer %q is not an option for decision %s", answer, d.ID)
	}
	if responder == "" {
		return errValidation("decision answer needs a responder")
	}
	if at.IsZero() {
		return errValidation("decision answer needs a timestamp")
	}
	if evidenceID == "" {
		return errValidation("decision answer needs an evidence id")
	}
	d.Status = DecisionAnswered
	d.Answer = answer
	d.Responder = responder
	d.AnsweredAt = &at
	d.EvidenceID = evidenceID
	d.Sensitive = d.Sensitive || sensitive
	return d.Validate()
}
