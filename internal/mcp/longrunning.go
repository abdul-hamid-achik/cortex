package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Long-running task tools: findings backlog, repository dossier, survey
// coverage, campaign work plans, detached jobs, and checkpoint resume. Each
// handler is thin — build the kernel, call it, return the report.

type findingInput struct {
	TaskID    string   `json:"taskId" jsonschema:"the case that owns the finding"`
	Operation string   `json:"operation" jsonschema:"add | list | triage | dismiss | convert"`
	FindingID string   `json:"findingId,omitempty" jsonschema:"finding id for triage, dismiss, convert"`
	Title     string   `json:"title,omitempty" jsonschema:"add: one-line title"`
	Kind      string   `json:"kind,omitempty" jsonschema:"add: bug | improvement | feature | question (default bug)"`
	Severity  string   `json:"severity,omitempty" jsonschema:"add: low | medium | high (default medium)"`
	Detail    string   `json:"detail,omitempty" jsonschema:"add: longer description"`
	Files     []string `json:"files,omitempty" jsonschema:"add: files involved"`
	Symbols   []string `json:"symbols,omitempty" jsonschema:"add: symbols involved"`
	Evidence  []string `json:"evidence,omitempty" jsonschema:"add: evidence ids from this case that back the finding"`
	Reason    string   `json:"reason,omitempty" jsonschema:"dismiss (required) / triage: why"`
	Status    string   `json:"status,omitempty" jsonschema:"list: filter open | triaged | converted | dismissed"`
	Actor     string   `json:"actor,omitempty" jsonschema:"stable person or agent identifier"`
	Mode      string   `json:"mode,omitempty" jsonschema:"convert: child case mode change | investigate | review (default change)"`
	Risk      string   `json:"risk,omitempty" jsonschema:"convert: child case risk (defaults to the finding severity)"`
	Surfaces  []string `json:"surfaces,omitempty" jsonschema:"convert: child case surfaces (default parent's)"`
	Sensitive bool     `json:"sensitive,omitempty" jsonschema:"add: mark the finding sensitive"`
	Workspace string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type dossierInput struct {
	Operation string   `json:"operation" jsonschema:"add | list | refresh"`
	TaskID    string   `json:"taskId,omitempty" jsonschema:"add: the active case supplying provenance"`
	EntryID   string   `json:"entryId,omitempty" jsonschema:"add: existing entry id to update"`
	Module    string   `json:"module,omitempty" jsonschema:"add: module/directory the entry describes; list: path prefix filter"`
	Kind      string   `json:"kind,omitempty" jsonschema:"architecture | invariant | hotspot | convention | question"`
	Title     string   `json:"title,omitempty" jsonschema:"add: one-line title"`
	Summary   string   `json:"summary,omitempty" jsonschema:"add: what the module does or which invariant it keeps"`
	Files     []string `json:"files,omitempty" jsonschema:"add: files the entry depends on (default the module)"`
	Evidence  []string `json:"evidence,omitempty" jsonschema:"add: evidence ids from the case"`
	Actor     string   `json:"actor,omitempty" jsonschema:"stable person or agent identifier"`
	StaleOnly bool     `json:"staleOnly,omitempty" jsonschema:"list: only entries whose files changed since they were written"`
	Limit     int      `json:"limit,omitempty" jsonschema:"list: max entries (default 50)"`
	Workspace string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type coverageInput struct {
	TaskID    string `json:"taskId" jsonschema:"the survey case"`
	All       bool   `json:"all,omitempty" jsonschema:"return every module instead of the first 50"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type workplanInput struct {
	TaskID    string                   `json:"taskId" jsonschema:"the parent (campaign) case"`
	Operation string                   `json:"operation" jsonschema:"add | list | next"`
	ItemID    string                   `json:"itemId,omitempty" jsonschema:"add: stable id (generated when empty); next: claim this specific item"`
	Goal      string                   `json:"goal,omitempty" jsonschema:"add: the delegable goal"`
	Mode      string                   `json:"mode,omitempty" jsonschema:"add: child case mode change | investigate | review (default change)"`
	Risk      string                   `json:"risk,omitempty" jsonschema:"add: low | medium | high"`
	Surfaces  []string                 `json:"surfaces,omitempty" jsonschema:"add: child case surfaces"`
	Files     []string                 `json:"files,omitempty" jsonschema:"add: files the item is expected to touch"`
	Criteria  []acceptanceCriterionArg `json:"criteria,omitempty" jsonschema:"add: acceptance criteria for the child case"`
	DependsOn []string                 `json:"dependsOn,omitempty" jsonschema:"add: item ids that must finish first"`
	Actor     string                   `json:"actor,omitempty" jsonschema:"next: the actor claiming the item (required)"`
	Workspace string                   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type jobInput struct {
	TaskID    string   `json:"taskId" jsonschema:"the case the job belongs to"`
	Operation string   `json:"operation" jsonschema:"start | list | cancel"`
	JobID     string   `json:"jobId,omitempty" jsonschema:"cancel: the job id"`
	Question  string   `json:"question,omitempty" jsonschema:"start: the question each round investigates (optional with fanout/modules)"`
	Depth     string   `json:"depth,omitempty" jsonschema:"start: quick | standard | deep"`
	Modules   []string `json:"modules,omitempty" jsonschema:"start: explicit modules, one bounded round each"`
	Fanout    bool     `json:"fanout,omitempty" jsonschema:"start: survey fan-out over the next unseen modules"`
	Max       int      `json:"max,omitempty" jsonschema:"start: fan-out size (default 5, max 32)"`
	Workspace string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type resumeInput struct {
	TaskID    string `json:"taskId" jsonschema:"the case to resume"`
	Since     string `json:"since,omitempty" jsonschema:"RFC3339 cursor; return only records after it"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max evidence records (default 50)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

func (s *Server) registerLongRunning() {
	sdkmcp.AddTool(s.srv, s.tool("cortex_finding", "Record or triage a finding",
		"Durable backlog for what a survey or investigation turns up. operation=add records a bug/improvement/feature/question backed by evidence ids from the case; list filters by status; triage acknowledges; dismiss records a reason (indexed for recall); convert opens a linked child case whose acceptance criterion is the finding.",
		toolBehavior{additive: true, idempotent: true}), s.handleFinding)
	sdkmcp.AddTool(s.srv, s.tool("cortex_dossier", "Repository memory",
		"Evidence-backed, per-module memory that outlives cases. operation=add writes or updates an entry from an active case (module, kind, title, summary, evidence ids); list returns entries with freshness evaluated against HEAD (stale when their files changed since written); refresh persists the stale marks.",
		toolBehavior{additive: true, idempotent: true}), s.handleDossier)
	sdkmcp.AddTool(s.srv, s.tool("cortex_coverage", "Read survey coverage",
		"For a survey case: the module ledger with unseen/explored/summarized state, fan-in, rounds, and the next module to visit. Progress is measured against this ledger; completion refuses while modules remain unseen unless acknowledged.",
		toolBehavior{readOnly: true, additive: true}), s.handleCoverage)
	sdkmcp.AddTool(s.srv, s.tool("cortex_workplan", "Plan and hand out campaign work",
		"Dependency-aware child work under a parent case. operation=add appends an item (goal, mode, criteria, dependsOn); list shows each item's state derived from its child case; next claims the first ready item for an actor by opening its linked, retry-keyed child case.",
		toolBehavior{additive: true, idempotent: true}), s.handleWorkplan)
	sdkmcp.AddTool(s.srv, s.tool("cortex_job", "Background investigation jobs",
		"Detached investigations that outlive the MCP call. operation=start queues one round (question) or a survey fan-out (fanout=true or modules) and launches a worker whose evidence lands in the case through the ordinary redacted path; list polls progress and reports dead workers as failed; cancel stops a worker and keeps recorded evidence.",
		toolBehavior{additive: true, openWorld: true}), s.handleJob)
	sdkmcp.AddTool(s.srv, s.tool("cortex_resume", "Resume after context loss",
		"Return the case checkpoint (compact what-I-know / what-I-am-doing / what-is-next packet, rewritten after every durable step) plus the deltas since an RFC3339 cursor: evidence, phase moves, pending decision, open findings, coverage, jobs, and stale evidence. Call it first after compaction or when taking over another actor's case.",
		toolBehavior{readOnly: true, additive: true}), s.handleResume)
}

func (s *Server) handleFinding(ctx context.Context, _ *sdkmcp.CallToolRequest, in findingInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "add", "":
		rep, err := k.RecordFinding(kernel.FindingInput{
			TaskID: in.TaskID, Kind: in.Kind, Severity: in.Severity, Title: in.Title, Detail: in.Detail,
			Files: in.Files, Symbols: in.Symbols, Evidence: in.Evidence, Actor: in.Actor, Sensitive: in.Sensitive,
		})
		return result(rep, err)
	case "list":
		rep, err := k.ListFindings(in.TaskID, in.Status)
		return result(rep, err)
	case "triage":
		rep, err := k.UpdateFindingStatus(ctx, kernel.FindingStatusInput{TaskID: in.TaskID, FindingID: in.FindingID, Status: "triaged", Reason: in.Reason, Actor: in.Actor})
		return result(rep, err)
	case "dismiss":
		rep, err := k.UpdateFindingStatus(ctx, kernel.FindingStatusInput{TaskID: in.TaskID, FindingID: in.FindingID, Status: "dismissed", Reason: in.Reason, Actor: in.Actor})
		return result(rep, err)
	case "convert":
		rep, err := k.ConvertFinding(ctx, kernel.ConvertFindingInput{TaskID: in.TaskID, FindingID: in.FindingID, Actor: in.Actor, Mode: in.Mode, Risk: in.Risk, Surfaces: toSurfaces(in.Surfaces)})
		return result(rep, err)
	default:
		return errResult("operation must be add, list, triage, dismiss, or convert"), nil, nil
	}
}

func (s *Server) handleDossier(ctx context.Context, _ *sdkmcp.CallToolRequest, in dossierInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "add":
		rep, err := k.UpsertDossierEntry(ctx, kernel.DossierEntryInput{
			TaskID: in.TaskID, EntryID: in.EntryID, Module: in.Module, Kind: in.Kind, Title: in.Title, Summary: in.Summary,
			Files: in.Files, Evidence: in.Evidence, Actor: in.Actor,
		})
		return result(rep, err)
	case "list", "":
		rep, err := k.Dossier(ctx, kernel.DossierListInput{Module: in.Module, Kind: in.Kind, StaleOnly: in.StaleOnly, Limit: in.Limit})
		return result(rep, err)
	case "refresh":
		rep, err := k.RefreshDossier(ctx)
		return result(rep, err)
	default:
		return errResult("operation must be add, list, or refresh"), nil, nil
	}
}

func (s *Server) handleCoverage(_ context.Context, _ *sdkmcp.CallToolRequest, in coverageInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	rep, err := k.Coverage(in.TaskID, in.All)
	return result(rep, err)
}

func (s *Server) handleWorkplan(ctx context.Context, _ *sdkmcp.CallToolRequest, in workplanInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "add":
		rep, err := k.AddWorkItem(kernel.WorkItemInput{
			TaskID: in.TaskID, ID: in.ItemID, Goal: in.Goal, Mode: in.Mode, Risk: in.Risk, Surfaces: toSurfaces(in.Surfaces),
			Files: in.Files, Criteria: toAcceptanceCriteria(in.Criteria), DependsOn: in.DependsOn,
		})
		return result(rep, err)
	case "list", "":
		rep, err := k.Workplan(in.TaskID)
		return result(rep, err)
	case "next":
		rep, err := k.NextWorkItem(ctx, kernel.NextWorkItemInput{TaskID: in.TaskID, Actor: in.Actor, ItemID: in.ItemID})
		return result(rep, err)
	default:
		return errResult("operation must be add, list, or next"), nil, nil
	}
}

func (s *Server) handleJob(ctx context.Context, _ *sdkmcp.CallToolRequest, in jobInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "start":
		rep, err := k.StartJob(ctx, kernel.JobInput{TaskID: in.TaskID, Question: in.Question, Depth: in.Depth, Modules: in.Modules, Fanout: in.Fanout, Max: in.Max})
		return result(rep, err)
	case "list", "":
		rep, err := k.ListJobs(ctx, in.TaskID)
		return result(rep, err)
	case "cancel":
		rep, err := k.CancelJob(ctx, in.TaskID, in.JobID)
		return result(rep, err)
	default:
		return errResult("operation must be start, list, or cancel"), nil, nil
	}
}

func (s *Server) handleResume(ctx context.Context, _ *sdkmcp.CallToolRequest, in resumeInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	var since time.Time
	if strings.TrimSpace(in.Since) != "" {
		parsed, pErr := time.Parse(time.RFC3339, strings.TrimSpace(in.Since))
		if pErr != nil {
			return errResult("since must be an RFC3339 timestamp: " + pErr.Error()), nil, nil
		}
		since = parsed
	}
	rep, err := k.Resume(ctx, kernel.ResumeInput{TaskID: in.TaskID, Since: since, Limit: in.Limit})
	return result(rep, err)
}
