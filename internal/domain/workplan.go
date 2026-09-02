package domain

import (
	"strings"
	"time"
)

// WorkItemStatus is the durable claim state of one campaign item. Completion
// is derived from the child case at read time; the plan only records who
// claimed the item and which case it became.
type WorkItemStatus string

const (
	WorkItemPending WorkItemStatus = "pending"
	WorkItemClaimed WorkItemStatus = "claimed"
)

// WorkItem is one delegable unit inside a parent campaign. DependsOn names
// other item IDs that must reach a terminal child case first.
type WorkItem struct {
	ID        string                `json:"id"`
	Goal      string                `json:"goal"`
	Mode      Mode                  `json:"mode"`
	Risk      string                `json:"risk,omitempty"`
	Surfaces  []Surface             `json:"surfaces,omitempty"`
	Files     []string              `json:"files,omitempty"`
	Criteria  []AcceptanceCriterion `json:"criteria,omitempty"`
	DependsOn []string              `json:"dependsOn,omitempty"`
	Status    WorkItemStatus        `json:"status"`
	TaskID    string                `json:"taskId,omitempty"`
	Actor     string                `json:"actor,omitempty"`
	CreatedAt time.Time             `json:"createdAt"`
	ClaimedAt *time.Time            `json:"claimedAt,omitempty"`
}

// Workplan is a parent case's ordered, dependency-aware list of child work.
// Cortex hands items out; it never runs the agents that take them.
type Workplan struct {
	SchemaVersion int        `json:"schemaVersion"`
	Items         []WorkItem `json:"items"`
}

// Find returns the item with the given ID or nil.
func (w *Workplan) Find(id string) *WorkItem {
	for i := range w.Items {
		if w.Items[i].ID == id {
			return &w.Items[i]
		}
	}
	return nil
}

// Validate enforces unique IDs, known dependencies, and an acyclic graph.
func (w Workplan) Validate() error {
	seen := make(map[string]bool, len(w.Items))
	for _, item := range w.Items {
		if item.ID == "" {
			return errValidation("work item has no id")
		}
		if strings.TrimSpace(item.Goal) == "" {
			return errValidation("work item " + item.ID + " has no goal")
		}
		if !item.Mode.Valid() {
			return errValidation("work item " + item.ID + " has an unknown mode")
		}
		if seen[item.ID] {
			return errValidation("work item ids must be unique: " + item.ID)
		}
		seen[item.ID] = true
	}
	for _, item := range w.Items {
		for _, dep := range item.DependsOn {
			if dep == item.ID {
				return errValidation("work item " + item.ID + " depends on itself")
			}
			if !seen[dep] {
				return errValidation("work item " + item.ID + " depends on unknown item " + dep)
			}
		}
	}
	// Cycle check: iterative DFS with colors.
	const white, grey, black = 0, 1, 2
	color := make(map[string]int, len(w.Items))
	byID := make(map[string]WorkItem, len(w.Items))
	for _, item := range w.Items {
		byID[item.ID] = item
	}
	var visit func(id string) bool
	visit = func(id string) bool {
		switch color[id] {
		case grey:
			return false
		case black:
			return true
		}
		color[id] = grey
		for _, dep := range byID[id].DependsOn {
			if !visit(dep) {
				return false
			}
		}
		color[id] = black
		return true
	}
	for _, item := range w.Items {
		if !visit(item.ID) {
			return errValidation("work plan has a dependency cycle through " + item.ID)
		}
	}
	return nil
}
