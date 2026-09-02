package domain

import (
	"strings"
	"time"
)

// FindingKind classifies what a survey or investigation turned up. Findings
// are durable observations awaiting triage — not hypotheses (which explain a
// symptom) and not verification (which proves a claim).
type FindingKind string

const (
	FindingBug         FindingKind = "bug"
	FindingImprovement FindingKind = "improvement"
	FindingFeature     FindingKind = "feature"
	FindingQuestion    FindingKind = "question"
)

// FindingKinds is the documented vocabulary, in display order.
var FindingKinds = []string{string(FindingBug), string(FindingImprovement), string(FindingFeature), string(FindingQuestion)}

// Valid reports whether k is a documented finding kind.
func (k FindingKind) Valid() bool {
	switch k {
	case FindingBug, FindingImprovement, FindingFeature, FindingQuestion:
		return true
	default:
		return false
	}
}

// FindingStatus tracks a finding from discovery to disposition.
type FindingStatus string

const (
	FindingOpen      FindingStatus = "open"
	FindingTriaged   FindingStatus = "triaged"
	FindingConverted FindingStatus = "converted"
	FindingDismissed FindingStatus = "dismissed"
)

// FindingStatuses is the documented vocabulary, in lifecycle order.
var FindingStatuses = []string{string(FindingOpen), string(FindingTriaged), string(FindingConverted), string(FindingDismissed)}

// Valid reports whether s is a documented finding status.
func (s FindingStatus) Valid() bool {
	switch s {
	case FindingOpen, FindingTriaged, FindingConverted, FindingDismissed:
		return true
	default:
		return false
	}
}

// Terminal reports whether a finding has reached a disposition.
func (s FindingStatus) Terminal() bool {
	return s == FindingConverted || s == FindingDismissed
}

// FindingSeverities is the documented severity vocabulary.
var FindingSeverities = []string{"low", "medium", "high"}

// Finding is one durable backlog item discovered while surveying or
// investigating a repository. Evidence IDs point into the owning case's
// ledger, so every finding stays provenance-bearing. A dismissed finding keeps
// its reason: "we looked and it is not a bug" is as valuable to recall as a
// rejected hypothesis.
type Finding struct {
	ID       string        `json:"id"`
	TaskID   string        `json:"taskId"`
	Kind     FindingKind   `json:"kind"`
	Severity string        `json:"severity"`
	Title    string        `json:"title"`
	Detail   string        `json:"detail,omitempty"`
	Files    []string      `json:"files,omitempty"`
	Symbols  []string      `json:"symbols,omitempty"`
	Evidence []string      `json:"evidence,omitempty"`
	Status   FindingStatus `json:"status"`
	// Reason records why the finding was dismissed or how it was triaged.
	Reason string `json:"reason,omitempty"`
	// ConvertedTaskID names the child case opened from this finding.
	ConvertedTaskID string    `json:"convertedTaskId,omitempty"`
	Actor           string    `json:"actor,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	Sensitive       bool      `json:"sensitive,omitempty"`
}

// Validate enforces the finding invariants: an owner case, a title, a
// documented kind, severity, and status, and a reason on every dismissal.
func (f Finding) Validate() error {
	if f.ID == "" {
		return errValidation("finding has no id")
	}
	if f.TaskID == "" {
		return errValidation("finding has no owning task")
	}
	if strings.TrimSpace(f.Title) == "" {
		return errValidation("finding has no title")
	}
	if !f.Kind.Valid() {
		return errValidation("finding kind must be one of: " + strings.Join(FindingKinds, ", "))
	}
	switch f.Severity {
	case "low", "medium", "high":
	default:
		return errValidation("finding severity must be low, medium, or high")
	}
	if !f.Status.Valid() {
		return errValidation("finding status must be one of: " + strings.Join(FindingStatuses, ", "))
	}
	if f.Status == FindingDismissed && strings.TrimSpace(f.Reason) == "" {
		return errValidation("a dismissed finding needs a reason")
	}
	if f.Status == FindingConverted && f.ConvertedTaskID == "" {
		return errValidation("a converted finding needs the child task id")
	}
	if f.CreatedAt.IsZero() {
		return errValidation("finding has no creation timestamp")
	}
	return nil
}

// CriterionID returns the stable acceptance-criterion id a converted finding
// contributes to its child case, so the child's proof is bound to the finding.
func (f Finding) CriterionID() string {
	return strings.ToLower(strings.ReplaceAll(f.ID, "-", "_"))
}
