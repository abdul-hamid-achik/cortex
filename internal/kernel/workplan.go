package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
)

// WorkItemInput adds one delegable unit to a parent campaign.
type WorkItemInput struct {
	TaskID    string
	ID        string // optional stable id; generated when empty
	Goal      string
	Mode      string
	Risk      string
	Surfaces  []domain.Surface
	Files     []string
	Criteria  []domain.AcceptanceCriterion
	DependsOn []string
}

// NextWorkItemInput hands the next ready item to an actor.
type NextWorkItemInput struct {
	TaskID string
	Actor  string
	ItemID string // claim a specific item instead of the first ready one
}

// WorkItemView is one item with its state derived from the child case.
type WorkItemView struct {
	domain.WorkItem
	// Derived: pending | blocked | claimed | active | done | failed
	Derived   string   `json:"derived"`
	BlockedBy []string `json:"blockedBy,omitempty"`
}

// WorkplanReport is the campaign view.
type WorkplanReport struct {
	domain.Envelope
	Items     []WorkItemView `json:"items"`
	Pending   int            `json:"pending"`
	Ready     int            `json:"ready"`
	Active    int            `json:"active"`
	Done      int            `json:"done"`
	Failed    int            `json:"failed"`
	Next      *WorkItemView  `json:"next,omitempty"`
	ChildTask string         `json:"childTaskId,omitempty"`
}

// AddWorkItem appends an item to the parent's plan. Dependencies must already
// exist and the graph must stay acyclic.
func (k *Kernel) AddWorkItem(in WorkItemInput) (WorkplanReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	if c.Status.IsTerminal() {
		return WorkplanReport{Envelope: k.errEnvelopeForCase(c, fmt.Sprintf("cannot plan work under a case in terminal phase %q", c.Status))}, nil
	}
	goal := strings.TrimSpace(in.Goal)
	if goal == "" {
		return WorkplanReport{Envelope: k.errEnvelopeActions(c.ID, "work item needs a goal", domain.NextAction{
			Tool: "cortex_workplan", Command: cortexCommand(c, "workplan", "add", c.ID, "GOAL"),
			Reason: "state the delegable goal", Arguments: cloneArgs(knownActionArgs(c), "operation", "add"), Inputs: []string{"goal"},
		})}, nil
	}
	if textExceeds(goal, maxGoalBytes) {
		return WorkplanReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("work item goal exceeds %d bytes", maxGoalBytes))}, nil
	}
	mode, ok := normalizeMode(domain.Mode(in.Mode))
	if !ok || mode == domain.ModeSurvey {
		return WorkplanReport{Envelope: k.errEnvelopeActions(c.ID, "work item mode must be change, investigate, or review", domain.NextAction{
			Tool: "cortex_workplan", Command: cortexCommand(c, "workplan", "add", c.ID, goal, "--mode", "MODE"),
			Reason: "choose the child case mode", Arguments: cloneArgs(knownActionArgs(c), "operation", "add", "goal", goal),
			Inputs: []string{"mode"}, Candidates: map[string][]string{"mode": {"change", "investigate", "review"}},
		})}, nil
	}
	risk, ok := normalizeRisk(in.Risk)
	if !ok {
		return WorkplanReport{Envelope: errEnvelope(c.ID, "work item risk must be low, medium, or high")}, nil
	}
	surfaces, err := normalizeSurfaces(in.Surfaces)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	criteria, err := k.normalizeAcceptanceCriteria(in.Criteria)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	if len(in.Files) > maxBoundaryEntries {
		return WorkplanReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("work item accepts at most %d files", maxBoundaryEntries))}, nil
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = ids.New("wi")
	}
	if textExceeds(id, maxStableIdentifierBytes) {
		return WorkplanReport{Envelope: errEnvelope(c.ID, "work item id is too long")}, nil
	}
	files := make([]string, 0, len(in.Files))
	for _, f := range in.Files {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, k.red.String(k.workspaceRelative(f)))
		}
	}
	deps := make([]string, 0, len(in.DependsOn))
	for _, d := range in.DependsOn {
		if d = strings.TrimSpace(d); d != "" {
			deps = append(deps, d)
		}
	}
	item := domain.WorkItem{
		ID: id, Goal: k.red.String(goal), Mode: mode, Risk: risk, Surfaces: surfaces, Files: files,
		Criteria: criteria, DependsOn: deps, Status: domain.WorkItemPending, CreatedAt: k.now().UTC(),
	}
	plan, err := k.store.UpdateWorkplan(c.ID, func(p *domain.Workplan) error {
		if p.Find(item.ID) != nil {
			return fmt.Errorf("work item %s already exists", item.ID)
		}
		p.Items = append(p.Items, item)
		return nil
	})
	if err != nil {
		return WorkplanReport{Envelope: k.errEnvelopeActions(c.ID, err.Error(), domain.NextAction{
			Tool: "cortex_workplan", Command: cortexCommand(c, "workplan", "list", c.ID),
			Reason: "inspect the existing plan and its ids", Arguments: cloneArgs(knownActionArgs(c), "operation", "list"),
			Candidates: map[string][]string{"dependsOn": k.workItemIDs(c.ID)},
		})}, nil
	}
	rep := k.workplanReport(c, plan)
	rep.Summary = fmt.Sprintf("work item %s added (%d items, %d ready)", item.ID, len(plan.Items), rep.Ready)
	return rep, nil
}

func (k *Kernel) workItemIDs(taskID string) []string {
	plan, err := k.store.LoadWorkplan(taskID)
	if err != nil {
		return nil
	}
	var out []string
	for _, item := range plan.Items {
		out = append(out, item.ID)
		if len(out) >= maxRejectionCandidates {
			break
		}
	}
	return out
}

// Workplan returns the campaign with derived item states.
func (k *Kernel) Workplan(taskID string) (WorkplanReport, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	plan, err := k.store.LoadWorkplan(c.ID)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	rep := k.workplanReport(c, plan)
	rep.Summary = fmt.Sprintf("campaign %s: %d items — %d done, %d active, %d ready, %d pending, %d failed",
		c.ID, len(plan.Items), rep.Done, rep.Active, rep.Ready, rep.Pending, rep.Failed)
	return rep, nil
}

func (k *Kernel) workplanReport(c *domain.CaseFile, plan domain.Workplan) WorkplanReport {
	views := k.deriveWorkItems(plan)
	rep := WorkplanReport{Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status}, Items: views}
	for i := range views {
		switch views[i].Derived {
		case "done":
			rep.Done++
		case "active", "claimed":
			rep.Active++
		case "failed":
			rep.Failed++
		case "blocked":
			rep.Pending++
		case "pending":
			rep.Ready++
			rep.Pending++
			if rep.Next == nil {
				next := views[i]
				rep.Next = &next
			}
		}
	}
	if rep.Next != nil && !c.Status.IsTerminal() {
		rep.Actions = append(rep.Actions, domain.NextAction{
			Tool: "cortex_workplan", Command: cortexCommand(c, "workplan", "next", c.ID, "--actor", "ACTOR"),
			Reason:    fmt.Sprintf("claim the next ready item (%s: %s) as a linked child case", rep.Next.ID, clipStr(rep.Next.Goal, 60)),
			Arguments: cloneArgs(knownActionArgs(c), "operation", "next"), Inputs: []string{"actor"},
		})
	} else if len(views) > 0 && rep.Active == 0 && rep.Pending == 0 && !c.Status.IsTerminal() {
		rep.Actions = append(rep.Actions, domain.NextAction{
			Tool: "cortex_status", Command: cortexCommand(c, "status", c.ID),
			Reason:    "every item reached a terminal child case; review the rollup and close the campaign",
			Arguments: knownActionArgs(c),
		})
	}
	return rep
}

// deriveWorkItems evaluates each item against its child case.
func (k *Kernel) deriveWorkItems(plan domain.Workplan) []WorkItemView {
	state := make(map[string]string, len(plan.Items))
	views := make([]WorkItemView, 0, len(plan.Items))
	for _, item := range plan.Items {
		derived := "pending"
		if item.TaskID != "" {
			derived = "claimed"
			if child, _, err := k.loadChildCase(item.TaskID); err == nil && child != nil {
				switch {
				case child.Status == domain.PhaseComplete:
					derived = "done"
				case child.Status.IsTerminal():
					derived = "failed"
				default:
					derived = "active"
				}
			}
		}
		state[item.ID] = derived
		views = append(views, WorkItemView{WorkItem: item, Derived: derived})
	}
	for i := range views {
		if views[i].Derived != "pending" {
			continue
		}
		for _, dep := range views[i].DependsOn {
			if state[dep] != "done" {
				views[i].BlockedBy = append(views[i].BlockedBy, dep)
			}
		}
		if len(views[i].BlockedBy) > 0 {
			views[i].Derived = "blocked"
		}
	}
	return views
}

// NextWorkItem claims the next ready item for an actor by opening its child
// case (retry-safe: the idempotency key is parent/item). Two actors racing for
// the same item resolve through the parent's task lock: the second sees the
// item claimed and receives the following ready item instead.
func (k *Kernel) NextWorkItem(ctx context.Context, in NextWorkItemInput) (WorkplanReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	if c.Status.IsTerminal() {
		return WorkplanReport{Envelope: k.errEnvelopeForCase(c, fmt.Sprintf("cannot hand out work under a case in terminal phase %q", c.Status))}, nil
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		return WorkplanReport{Envelope: k.errEnvelopeActions(c.ID, "next needs a stable actor", domain.NextAction{
			Tool: "cortex_workplan", Command: cortexCommand(c, "workplan", "next", c.ID, "--actor", "ACTOR"),
			Reason: "name who is taking the item", Arguments: cloneArgs(knownActionArgs(c), "operation", "next"), Inputs: []string{"actor"},
		})}, nil
	}
	if textExceeds(actor, maxStableIdentifierBytes) {
		return WorkplanReport{Envelope: errEnvelope(c.ID, "actor exceeds the identifier bound")}, nil
	}
	plan, err := k.store.LoadWorkplan(c.ID)
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	views := k.deriveWorkItems(plan)
	var chosen *WorkItemView
	for i := range views {
		if in.ItemID != "" && views[i].ID != strings.TrimSpace(in.ItemID) {
			continue
		}
		if views[i].Derived == "pending" {
			chosen = &views[i]
			break
		}
		if in.ItemID != "" {
			return WorkplanReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("work item %s is %s, not ready", views[i].ID, views[i].Derived))}, nil
		}
	}
	if chosen == nil {
		rep := k.workplanReport(c, plan)
		rep.OK = true
		rep.Summary = "no ready work item: every item is claimed, blocked, or finished"
		return rep, nil
	}
	surfaces := chosen.Surfaces
	if len(surfaces) == 0 {
		surfaces = append([]domain.Surface(nil), c.Surfaces...)
	}
	child, err := k.OpenTask(ctx, OpenInput{StartInput: StartInput{
		Goal: chosen.Goal, Mode: chosen.Mode, Surfaces: surfaces, Risk: chosen.Risk,
		AcceptanceCriteria: chosen.Criteria, Actor: actor, ParentTaskID: c.ID,
		IdempotencyKey: c.ID + "/" + chosen.ID,
	}})
	if err != nil {
		return WorkplanReport{Envelope: child}, err
	}
	if !child.OK || child.TaskID == "" {
		return WorkplanReport{Envelope: child}, nil
	}
	now := k.now().UTC()
	plan, err = k.store.UpdateWorkplan(c.ID, func(p *domain.Workplan) error {
		item := p.Find(chosen.ID)
		if item == nil {
			return fmt.Errorf("work item %s vanished", chosen.ID)
		}
		if item.TaskID != "" && item.TaskID != child.TaskID {
			return fmt.Errorf("work item %s was claimed by %s meanwhile", item.ID, item.Actor)
		}
		item.Status, item.TaskID, item.Actor, item.ClaimedAt = domain.WorkItemClaimed, child.TaskID, k.red.String(actor), &now
		return nil
	})
	if err != nil {
		return WorkplanReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	rep := k.workplanReport(c, plan)
	rep.ChildTask = child.TaskID
	rep.Summary = fmt.Sprintf("work item %s claimed by %s as child case %s (%s)", chosen.ID, actor, child.TaskID, child.Phase)
	rep.Warnings = append(rep.Warnings, child.Warnings...)
	rep.Actions = append(child.Actions, rep.Actions...)
	rep.NextActions = child.NextActions
	return rep, nil
}
