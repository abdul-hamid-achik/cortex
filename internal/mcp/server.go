// Package mcp exposes Cortex over the Model Context Protocol (stdio). It is a
// THIN layer: every handler resolves arguments, builds a workspace-scoped
// kernel, delegates to internal/kernel, and returns JSON. It uses the official
// go-sdk, whose StdioTransport emits newline-delimited JSON-RPC (required by
// Claude Code / Codex — Content-Length framing makes them report "failed to
// connect"). All logging must go to stderr so stdout stays pure JSON-RPC.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/abdul-hamid-achik/cortex/internal/version"
)

const instructions = `Cortex is an evidence-guided agent kernel. It gives non-trivial engineering
work a durable case file, disciplined tool routing, bounded changes, and verification tied to
user-visible behavior. Follow the actions returned in each JSON result; they are executable
continuations, while the inputs field lists values you must still supply.

1. cortex_open_task — preferred entry point: retry-safe resume-or-start for a goal. Supply an
   idempotencyKey when a lost response may be retried. When success rules are already known,
   register acceptanceCriteria and later verify each with the same claim id and exact statement.
   Use cortex_start_task only when you deliberately want a fresh case.
2. cortex_investigate — route a question through discovery then structure and retain evidence
   IDs. Treat search output as candidates, NOT proof.
3. cortex_plan — before editing, state hypotheses WITH disproof paths, uncertainty, a change
   boundary for change tasks, and required verification. Invalid plans are rejected.
4. cortex_begin_change — for a planned change, name a stable actor, claim the bounded lease,
   then edit only within the declared boundary. Same-actor retries are safe.
5. cortex_verify — after editing, pass the lease actor and prefer typed claimSpecs with explicit
   surfaces and exact contracts. Reuse registered acceptance-criterion ids/statements exactly.
   Cortex runs relevant verifiers, detects scope drift, and records
   receipts; a claim with no verifier is not_run, never passed. Repository-configured commands run
   only when the trusted launcher set CORTEX_APPROVE_COMMANDS=1; otherwise they are blocked.
6. cortex_remember — persist the outcome. Normal completion requires the canonical assessment to
   be verified; verificationNotPossible explicitly preserves partial/unverified work, while
   acceptFailed explicitly preserves a failed outcome.

Use cortex_status for current state; cortex_resolve for hypothesis outcomes; cortex_note for
provenance-bearing human/agent context; cortex_request_decision and cortex_answer_decision for a
resumable human pause; cortex_handoff for a bounded transfer packet; cortex_read_evidence and
cortex_read_artifact for progressively deeper evidence; and cortex_abort_task to stop without
deleting evidence. The all profile additionally exposes cross-repository operator views and
archive controls. Never request or expose secret values — Cortex checks capability only.`

// Server wraps the go-sdk MCP server. Kernels are built per-call so one server
// process can serve tasks in any workspace the tools name.
type Server struct {
	defaultWorkspace string
	profile          Profile
	envelopeSchema   *jsonschema.Schema
	srv              *sdkmcp.Server
}

// Profile controls tool exposure without changing the shared kernel. Agent
// mode keeps lifecycle/evidence tools and hides cross-repo administration;
// all preserves the historical full surface for operators and compatibility.
type Profile string

const (
	ProfileAgent Profile = "agent"
	ProfileAll   Profile = "all"
)

// NewServer builds an MCP server defaulting to the given workspace directory.
func NewServer(defaultWorkspace string) *Server {
	s, _ := NewServerWithProfile(defaultWorkspace, string(ProfileAll))
	return s
}

// NewServerWithProfile builds a server with a validated exposure profile.
func NewServerWithProfile(defaultWorkspace, rawProfile string) (*Server, error) {
	profile := Profile(strings.ToLower(strings.TrimSpace(rawProfile)))
	if profile == "" {
		profile = ProfileAgent
	}
	if profile != ProfileAgent && profile != ProfileAll {
		return nil, fmt.Errorf("profile must be agent or all")
	}
	envelopeSchema, err := jsonschema.For[domain.Envelope](&jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("build envelope output schema: %w", err)
	}
	s := &Server{defaultWorkspace: defaultWorkspace, profile: profile, envelopeSchema: envelopeSchema}
	s.srv = sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "cortex", Version: version.Version},
		&sdkmcp.ServerOptions{Instructions: instructions},
	)
	s.register()
	return s, nil
}

// Run serves over stdio until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.serve(ctx, &sdkmcp.StdioTransport{})
}

// serve runs over an arbitrary transport (tests use an in-memory one).
func (s *Server) serve(ctx context.Context, t sdkmcp.Transport) error {
	return s.srv.Run(ctx, t)
}

func (s *Server) kernelFor(workspace string) (*kernel.Kernel, error) {
	ws := workspace
	if ws == "" {
		ws = s.defaultWorkspace
	}
	return kernel.New(config.For(ws))
}

// ---- tool inputs ----

type startInput struct {
	Goal               string                   `json:"goal" jsonschema:"the engineering goal for this task"`
	Workspace          string                   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
	Mode               string                   `json:"mode,omitempty" jsonschema:"change | investigate | review (default change)"`
	Surfaces           []string                 `json:"surfaces,omitempty" jsonschema:"user-visible surfaces: code, browser, terminal, artifact, secret"`
	Risk               string                   `json:"risk,omitempty" jsonschema:"low | medium | high (default medium)"`
	AcceptanceCriteria []acceptanceCriterionArg `json:"acceptanceCriteria,omitempty" jsonschema:"optional immutable success contract; prove each criterion with a claimSpec using the same id and exact statement"`
}

type openTaskInput struct {
	Goal               string                   `json:"goal" jsonschema:"the engineering goal to resume or start"`
	Workspace          string                   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
	Mode               string                   `json:"mode,omitempty" jsonschema:"change | investigate | review (default change)"`
	Surfaces           []string                 `json:"surfaces,omitempty" jsonschema:"user-visible surfaces: code, browser, terminal, artifact, secret"`
	Risk               string                   `json:"risk,omitempty" jsonschema:"low | medium | high (default medium)"`
	Actor              string                   `json:"actor,omitempty" jsonschema:"stable non-secret person or agent identifier"`
	ParentTaskID       string                   `json:"parentTaskId,omitempty" jsonschema:"parent case when this task was delegated"`
	IdempotencyKey     string                   `json:"idempotencyKey,omitempty" jsonschema:"stable retry key; exact matches return the existing task even after completion"`
	AcceptanceCriteria []acceptanceCriterionArg `json:"acceptanceCriteria,omitempty" jsonschema:"optional immutable success contract; retries and automatic resume must supply the same criteria"`
}

type acceptanceCriterionArg struct {
	ID        string `json:"id" jsonschema:"stable non-secret criterion id; use the same id in verify.claimSpecs"`
	Statement string `json:"statement" jsonschema:"the exact success statement that verification must prove"`
}

type beginChangeInput struct {
	TaskID    string `json:"taskId" jsonschema:"the planned change task"`
	Actor     string `json:"actor" jsonschema:"stable non-secret agent/person taking change ownership"`
	TTL       string `json:"ttl,omitempty" jsonschema:"bounded lease duration such as 15m or 1h (default 15m)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type investigateInput struct {
	TaskID    string   `json:"taskId" jsonschema:"the task to investigate under"`
	Question  string   `json:"question" jsonschema:"the question to route through discovery + structural tools"`
	Surfaces  []string `json:"surfaces,omitempty" jsonschema:"override the surfaces to consider for routing"`
	Depth     string   `json:"depth,omitempty" jsonschema:"quick | standard | deep (default standard)"`
	Video     string   `json:"video,omitempty" jsonschema:"a vidtrace bundle DIRECTORY or vidtrace stash id (NOT a raw .mp4/.mov file): runs vidtrace to link the visible failure to code. Build a bundle from a raw recording first with 'vidtrace extract <file> -json'"`
	Workspace string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type hypothesisArg struct {
	Statement  string   `json:"statement" jsonschema:"the falsifiable explanation"`
	Supports   []string `json:"supports,omitempty" jsonschema:"evidence IDs that support it"`
	Confidence string   `json:"confidence,omitempty" jsonschema:"high | medium | low | unknown"`
	DisproveBy string   `json:"disproveBy" jsonschema:"what result would disprove this (required — plans without a disproof path are rejected)"`
}

type planInput struct {
	TaskID           string            `json:"taskId" jsonschema:"the task to plan"`
	Hypotheses       []hypothesisArg   `json:"hypotheses" jsonschema:"one or more hypotheses, each with a disproof path"`
	Files            []string          `json:"files,omitempty" jsonschema:"files you expect to change (the change boundary)"`
	Symbols          []string          `json:"symbols,omitempty" jsonschema:"symbols you expect to change"`
	BoundaryReason   string            `json:"boundaryReason,omitempty" jsonschema:"why these files/symbols are the expected change set"`
	Verification     []string          `json:"verification,omitempty" jsonschema:"required verifiers (e.g. codemap_review, cairntrace_flow)"`
	Uncertainty      string            `json:"uncertainty" jsonschema:"explicit statement of what remains uncertain (required)"`
	TimeoutOverrides map[string]string `json:"timeoutOverrides,omitempty" jsonschema:"per-task timeout override as tool→duration (e.g. {\"codemap\":\"45s\"}) — written to the case file"`
	Workspace        string            `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type verificationClaimArg struct {
	ID        string `json:"id,omitempty" jsonschema:"stable claim id; Cortex derives one when omitted"`
	Statement string `json:"statement" jsonschema:"the exact user-visible assertion to prove"`
	Surface   string `json:"surface" jsonschema:"code | browser | terminal | artifact | secret"`
	Verifier  string `json:"verifier,omitempty" jsonschema:"exact verifier; defaults from surface (code may use command:<configured-name>)"`
	Contract  string `json:"contract" jsonschema:"required exact spec path, configured check, or capability selector this claim must bind to"`
}

type verifyInput struct {
	TaskID           string                 `json:"taskId" jsonschema:"the task to verify"`
	Actor            string                 `json:"actor,omitempty" jsonschema:"active change-lease owner when the task is leased"`
	Claims           []string               `json:"claims,omitempty" jsonschema:"legacy free-text claims; prefer claimSpecs so verifier routing is explicit"`
	ClaimSpecs       []verificationClaimArg `json:"claimSpecs,omitempty" jsonschema:"typed claims bound to an explicit surface and required exact contract; verifier may default from surface"`
	ChangedFiles     []string               `json:"changedFiles,omitempty" jsonschema:"changed files; derived from git when omitted"`
	BrowserSpec      string                 `json:"browserSpec,omitempty" jsonschema:"cairntrace spec path to prove browser claims"`
	TerminalSpec     string                 `json:"terminalSpec,omitempty" jsonschema:"glyphrun spec path to prove terminal claims"`
	ArtifactRef      string                 `json:"artifactRef,omitempty" jsonschema:"fcheap stash ID or fcheap:// URI to prove an artifact claim"`
	SecretProject    string                 `json:"secretProject,omitempty" jsonschema:"tvault project whose value-free availability proves secret capability"`
	DisableAutoSpecs bool                   `json:"disableAutoSpecs,omitempty" jsonschema:"skip auto-selection of covering browser/terminal specs"`
	NoOpAcknowledged bool                   `json:"noOpAcknowledged,omitempty" jsonschema:"explicitly acknowledge that this change task intentionally produced no diff"`
	Workspace        string                 `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type rememberInput struct {
	TaskID                  string   `json:"taskId" jsonschema:"the task to complete"`
	Outcome                 string   `json:"outcome" jsonschema:"a concise, provenance-rich outcome summary"`
	Importance              float64  `json:"importance,omitempty" jsonschema:"0..1 importance for durable memory (default 0.5)"`
	Tags                    []string `json:"tags,omitempty" jsonschema:"tags for recall"`
	VerificationNotPossible bool     `json:"verificationNotPossible,omitempty" jsonschema:"explicitly acknowledge a partial or unverified assessment when adequate verification could not be completed"`
	AcceptFailed            bool     `json:"acceptFailed,omitempty" jsonschema:"explicitly acknowledge and preserve a canonical failed verification assessment"`
	Workspace               string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type listTasksInput struct {
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type sessionsInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"only sessions whose repository or slug contains this substring"`
	Active bool   `json:"active,omitempty" jsonschema:"only in-flight (non-terminal) sessions"`
	Query  string `json:"query,omitempty" jsonschema:"case-insensitive AND search across task id, goal, phase, mode, repository, workspace, and verification outcome"`
}

type timelineInput struct {
	TaskID    string `json:"taskId" jsonschema:"the task/session whose activity feed to return"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory fallback for repo-local/custom case stores"`
}

type metricsInput struct {
	TaskID    string `json:"taskId,omitempty" jsonschema:"a task to summarize; omit for the workspace aggregate"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type overviewInput struct {
	StaleAfterHours int `json:"staleAfterHours,omitempty" jsonschema:"hours an in-flight session may sit untouched before it counts as stale (default 24)"`
}

type archiveInput struct {
	TaskID string `json:"taskId" jsonschema:"the terminal session to archive"`
}

type unarchiveInput struct {
	TaskID string `json:"taskId" jsonschema:"the archived session to restore"`
}

type statusInput struct {
	TaskID    string `json:"taskId" jsonschema:"the task to report on"`
	Detail    string `json:"detail,omitempty" jsonschema:"standard | full (full adds tool health)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type resolveInput struct {
	TaskID       string   `json:"taskId" jsonschema:"the task the hypothesis belongs to"`
	HypothesisID string   `json:"hypothesisId" jsonschema:"the hypothesis to resolve (from a plan/status result)"`
	Status       string   `json:"status" jsonschema:"confirmed | challenged | rejected"`
	Reason       string   `json:"reason" jsonschema:"what evidence changed the status (required)"`
	Evidence     []string `json:"evidence,omitempty" jsonschema:"supporting/contradicting evidence IDs. Optional even for 'confirmed': if you have no formal evidence ID (e.g. proof was an ad hoc repro), omit it and describe the proof in reason — an evidence record is auto-minted from the reason"`
	Workspace    string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type observationInput struct {
	TaskID     string `json:"taskId" jsonschema:"the active task to attach this observation to"`
	Claim      string `json:"claim" jsonschema:"the redacted observation, constraint, decision context, or handoff fact"`
	Category   string `json:"category,omitempty" jsonschema:"observation | decision | constraint | handoff (default observation)"`
	Origin     string `json:"origin,omitempty" jsonschema:"human | agent | reviewer (default human)"`
	Actor      string `json:"actor,omitempty" jsonschema:"person or agent that supplied the observation"`
	URI        string `json:"uri,omitempty" jsonschema:"non-secret provenance URI"`
	Confidence string `json:"confidence,omitempty" jsonschema:"low | medium; prose observations can never be high-confidence proof"`
	Sensitive  bool   `json:"sensitive,omitempty" jsonschema:"mark this observation as sensitive even if automatic detection does not"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type decisionOptionArg struct {
	ID          string `json:"id" jsonschema:"stable non-secret option id"`
	Label       string `json:"label" jsonschema:"short human-readable choice"`
	Consequence string `json:"consequence" jsonschema:"trade-off or consequence of choosing this option"`
}

type requestDecisionInput struct {
	TaskID    string              `json:"taskId" jsonschema:"the active task to pause"`
	Question  string              `json:"question" jsonschema:"one bounded question a human must answer"`
	Options   []decisionOptionArg `json:"options" jsonschema:"at least two options with explicit consequences"`
	Requester string              `json:"requester" jsonschema:"agent or person requesting the decision"`
	Workspace string              `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type answerDecisionInput struct {
	TaskID     string `json:"taskId" jsonschema:"the paused task"`
	DecisionID string `json:"decisionId,omitempty" jsonschema:"pending decision id; omit only when resume=true after crash recovery"`
	Answer     string `json:"answer,omitempty" jsonschema:"selected option id"`
	Responder  string `json:"responder,omitempty" jsonschema:"human or actor selecting the option"`
	Resume     bool   `json:"resume,omitempty" jsonschema:"recover an already-answered decision whose case did not resume after a crash"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type handoffInput struct {
	TaskID    string `json:"taskId" jsonschema:"the task/session to export as a bounded transfer packet"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory fallback for repo-local/custom case stores"`
}

type abortInput struct {
	TaskID    string `json:"taskId" jsonschema:"the task to abort"`
	Reason    string `json:"reason" jsonschema:"why the task is being stopped (required)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type readEvidenceInput struct {
	TaskID     string `json:"taskId" jsonschema:"the task the evidence belongs to"`
	EvidenceID string `json:"evidenceId" jsonschema:"the evidence record ID (from an investigate/verify result)"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type readArtifactInput struct {
	TaskID      string `json:"taskId" jsonschema:"the exact task that owns or references the artifact"`
	Ref         string `json:"ref" jsonschema:"a task-owned case://…/raw/… ref or task-referenced fcheap://stash/… ref"`
	Path        string `json:"path,omitempty" jsonschema:"safe relative file path inside an fcheap stash; defaults to bounded file discovery"`
	MaxBytes    int    `json:"maxBytes,omitempty" jsonschema:"maximum source bytes to return; defaults to 32768 and is hard-capped at 131072"`
	AllowBinary bool   `json:"allowBinary,omitempty" jsonschema:"explicitly allow bounded binary content as base64; false rejects binary"`
	Workspace   string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type recallCasesInput struct {
	Query     string `json:"query" jsonschema:"the question/goal to recall prior resolved cases for"`
	Repo      string `json:"repo,omitempty" jsonschema:"scope to a repository name (empty = cross-repo)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max prior cases to return (default 5)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

// toolBehavior projects standard MCP safety and execution hints. These hints
// let clients built around different models present consistent approval UX and
// choose read-only tools without reverse-engineering Cortex's descriptions.
// They complement kernel enforcement; clients must not treat them as policy.
type toolBehavior struct {
	readOnly       bool
	additive       bool
	idempotent     bool
	openWorld      bool
	sharedEnvelope bool
}

func (s *Server) tool(name, title, description string, behavior toolBehavior) *sdkmcp.Tool {
	destructive := !behavior.readOnly && !behavior.additive
	openWorld := behavior.openWorld
	tool := &sdkmcp.Tool{
		Name: name, Title: title, Description: description,
		Annotations: &sdkmcp.ToolAnnotations{
			Title: title, ReadOnlyHint: behavior.readOnly,
			DestructiveHint: &destructive, IdempotentHint: behavior.idempotent,
			OpenWorldHint: &openWorld,
		},
	}
	if behavior.sharedEnvelope {
		tool.OutputSchema = s.envelopeSchema
	}
	return tool
}

func (s *Server) register() {
	sdkmcp.AddTool(s.srv, s.tool("cortex_start_task", "Start a fresh task",
		"Create a case file for a non-trivial engineering task and perform lightweight orientation (git identity + tool health). acceptanceCriteria optionally registers an immutable success contract. Returns the task ID and the recommended next action.",
		toolBehavior{additive: true, openWorld: true, sharedEnvelope: true}), s.handleStart)
	sdkmcp.AddTool(s.srv, s.tool("cortex_open_task", "Open or resume a task",
		"Idempotently resume matching work or start it once. An idempotencyKey survives response loss; otherwise the newest active case with the same normalized goal, mode, workspace, branch, and acceptanceCriteria is resumed. acceptanceCriteria is an optional immutable success contract.",
		toolBehavior{additive: true, idempotent: true, openWorld: true, sharedEnvelope: true}), s.handleOpen)
	sdkmcp.AddTool(s.srv, s.tool("cortex_investigate", "Investigate a task",
		"Route a question through discovery (vecgrep) then structure (codemap), record the returned evidence with provenance, and return a bounded summary. Search output is recorded as candidates, not proof.",
		toolBehavior{additive: true, openWorld: true, sharedEnvelope: true}), s.handleInvestigate)
	sdkmcp.AddTool(s.srv, s.tool("cortex_plan", "Plan a bounded change",
		"The planning gate. Store hypotheses (each REQUIRES a disproof path), a change boundary (files/symbols), and a verification plan. Rejects plans with no disproof path or (for change tasks) no boundary. Not a code generator.",
		toolBehavior{sharedEnvelope: true}), s.handlePlan)
	sdkmcp.AddTool(s.srv, s.tool("cortex_begin_change", "Begin a bounded change",
		"After planning and before editing, atomically claim a bounded change lease and enter changing. Competing actors are rejected; the same owner may safely retry.",
		toolBehavior{sharedEnvelope: true}), s.handleBeginChange)
	sdkmcp.AddTool(s.srv, s.tool("cortex_verify", "Verify task claims",
		"Run verification after editing, passing actor when a change lease is active. Prefer typed claimSpecs with an explicit surface and exact contract (verifier may default from surface). Repository-configured commands require trusted-launcher CORTEX_APPROVE_COMMANDS=1 or produce blocked receipts. Runs relevant checks and scope-drift detection; an unverified claim is never passed.",
		toolBehavior{openWorld: true, sharedEnvelope: true}), s.handleVerify)
	sdkmcp.AddTool(s.srv, s.tool("cortex_remember", "Preserve the task outcome",
		"Persist a concise outcome and complete the task. Normal completion requires the canonical assessment to be verified; verificationNotPossible explicitly accepts partial/unverified completion, while acceptFailed explicitly accepts a failed outcome.",
		toolBehavior{idempotent: true, openWorld: true, sharedEnvelope: true}), s.handleRemember)
	sdkmcp.AddTool(s.srv, s.tool("cortex_status", "Read task status",
		"Report a task's canonical verification outcome, bounded claimProofs for stable claim ids, unresolved hypotheses, scope drift, missing verification, and (with detail=full) tool health.",
		toolBehavior{readOnly: true, additive: true}), s.handleStatus)
	if s.profile == ProfileAll {
		sdkmcp.AddTool(s.srv, s.tool("cortex_list_tasks", "List workspace tasks",
			"List all tasks in the workspace (newest first): id, goal, phase, repository, createdAt.",
			toolBehavior{readOnly: true, additive: true}), s.handleListTasks)
		sdkmcp.AddTool(s.srv, s.tool("cortex_sessions", "List all sessions",
			"List Cortex sessions across EVERY repository (the central XDG audit view), newest first: id, goal, phase, mode, repository, slug, verified/required verification counts, active flag, timestamps. Workspace-independent — use it to see everything you have open or left unfinished anywhere. Filter with repo (substring), active (in-flight only), and query (case-insensitive AND terms across identity, goal, state, repo, workspace, and outcome).",
			toolBehavior{readOnly: true, additive: true}), s.handleSessions)
		sdkmcp.AddTool(s.srv, s.tool("cortex_timeline", "Read a session timeline",
			"Return a session's chronological activity feed — phase transitions, evidence, audited tool calls, and verification receipts merged and time-sorted. Located centrally by task ID; workspace is a fallback for repo-local/custom stores.",
			toolBehavior{readOnly: true, additive: true}), s.handleTimeline)
		sdkmcp.AddTool(s.srv, s.tool("cortex_metrics", "Read task metrics",
			"Observability metrics focused on outcomes and the evidence trail, not tool-call volume. With taskId — that task's tool calls, calls-before-first-evidence, verification coverage, time-in-phase, and each tool's contribution. Without taskId — workspace aggregate (completion/verified rates, mean tools & time to complete).",
			toolBehavior{readOnly: true, additive: true}), s.handleMetrics)
		sdkmcp.AddTool(s.srv, s.tool("cortex_overview", "Read the session overview",
			"Cross-repository rollup of EVERY Cortex session: totals, active/stale counts, completion & verified-completion rates, mean time to complete, and a per-repo breakdown. Workspace-independent — the 'what's my overall state across all repos' view.",
			toolBehavior{readOnly: true, additive: true}), s.handleOverview)
		sdkmcp.AddTool(s.srv, s.tool("cortex_archive", "Archive a session",
			"Archive a terminal (complete/abandoned/blocked) session — MOVE it out of the active tree to the archive (reversible via cortex_unarchive; nothing is deleted). Refuses in-flight sessions. Workspace-independent; located by task ID.",
			toolBehavior{idempotent: true}), s.handleArchive)
		sdkmcp.AddTool(s.srv, s.tool("cortex_unarchive", "Restore an archived session",
			"Restore an archived session back into the active tree. Workspace-independent; located by task ID.",
			toolBehavior{idempotent: true}), s.handleUnarchive)
	}
	sdkmcp.AddTool(s.srv, s.tool("cortex_resolve", "Resolve a hypothesis",
		"Update a hypothesis's status as evidence accumulates (confirmed/challenged/rejected). History is retained and the resolution is appended to the evidence ledger — this is how contradicting evidence is handled without silently overwriting a prior explanation.",
		toolBehavior{idempotent: true, openWorld: true, sharedEnvelope: true}), s.handleResolve)
	sdkmcp.AddTool(s.srv, s.tool("cortex_note", "Add a task note",
		"Record redacted human, reviewer, or agent context as provenance-bearing human_report evidence. Notes can inform reasoning but can never satisfy verification by themselves.",
		toolBehavior{additive: true, sharedEnvelope: true}), s.handleObservation)
	sdkmcp.AddTool(s.srv, s.tool("cortex_request_decision", "Request a human decision",
		"Pause an active case on one bounded human question with at least two explicit options and consequences. The case remains active in needs_human_decision and cannot advance until answered.",
		toolBehavior{idempotent: true, sharedEnvelope: true}), s.handleRequestDecision)
	sdkmcp.AddTool(s.srv, s.tool("cortex_answer_decision", "Answer a pending decision",
		"Record a human-selected option and resume the exact phase that was paused. Set resume=true only to recover an answer persisted just before a process crash.",
		toolBehavior{idempotent: true, sharedEnvelope: true}), s.handleAnswerDecision)
	sdkmcp.AddTool(s.srv, s.tool("cortex_handoff", "Read a task handoff",
		"Return a bounded transfer packet with coordination metadata, current plan, hypotheses, recent evidence, current verifier/named-claim receipts, decisions, and executable actions. Workspace is a repo-local/custom-store fallback; raw output is excluded.",
		toolBehavior{readOnly: true, additive: true}), s.handleHandoff)
	sdkmcp.AddTool(s.srv, s.tool("cortex_abort_task", "Abort a task",
		"Stop the active task without deleting its evidence. Requires a reason.",
		toolBehavior{idempotent: true, sharedEnvelope: true}), s.handleAbort)
	sdkmcp.AddTool(s.srv, s.tool("cortex_read_evidence", "Read evidence",
		"Return a full evidence record by ID. When its rawRef contains /raw/, fetch that bounded detail with cortex_read_artifact; self-pointing human/decision evidence has no separate raw output.",
		toolBehavior{readOnly: true, additive: true}), s.handleReadEvidence)
	sdkmcp.AddTool(s.srv, s.tool("cortex_read_artifact", "Preview an evidence artifact",
		"Return a bounded, redacted preview of a task-owned case rawRef or a fcheap stash already referenced by that task. path must be safe and relative; discovery is capped at 512 entries and 100 files. Binary is refused unless allowBinary is true.",
		toolBehavior{readOnly: true, additive: true}), s.handleReadArtifact)
	sdkmcp.AddTool(s.srv, s.tool("cortex_recall_cases", "Recall related cases",
		"Recall prior resolved cases (rejected/challenged hypotheses and definitive receipts) related to a query, across repos or scoped to one. Returns low-confidence model_inference evidence — prior disproofs to read before re-deriving a theory. Missing veclite returns an empty success; other recall failures return an error envelope.",
		toolBehavior{readOnly: true, additive: true, openWorld: true, sharedEnvelope: true}), s.handleRecallCases)
}

// ---- handlers (thin: build kernel, call kernel, return JSON) ----

func (s *Server) handleStart(ctx context.Context, _ *sdkmcp.CallToolRequest, in startInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.StartTask(ctx, kernel.StartInput{
		Goal: in.Goal, Mode: domain.Mode(in.Mode),
		Surfaces: toSurfaces(in.Surfaces), Risk: in.Risk,
		AcceptanceCriteria: toAcceptanceCriteria(in.AcceptanceCriteria),
	})
	return result(env, err)
}

func (s *Server) handleOpen(ctx context.Context, _ *sdkmcp.CallToolRequest, in openTaskInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.OpenTask(ctx, kernel.OpenInput{StartInput: kernel.StartInput{
		Goal: in.Goal, Mode: domain.Mode(in.Mode), Surfaces: toSurfaces(in.Surfaces), Risk: in.Risk,
		Actor: in.Actor, ParentTaskID: in.ParentTaskID, IdempotencyKey: in.IdempotencyKey,
		AcceptanceCriteria: toAcceptanceCriteria(in.AcceptanceCriteria),
	}})
	return result(env, err)
}

func (s *Server) handleInvestigate(ctx context.Context, _ *sdkmcp.CallToolRequest, in investigateInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.Investigate(ctx, kernel.InvestigateInput{
		TaskID: in.TaskID, Question: in.Question, Surfaces: toSurfaces(in.Surfaces), Depth: in.Depth, Video: in.Video,
	})
	return result(env, err)
}

func (s *Server) handlePlan(_ context.Context, _ *sdkmcp.CallToolRequest, in planInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	hyps := make([]kernel.HypothesisInput, 0, len(in.Hypotheses))
	for _, h := range in.Hypotheses {
		hyps = append(hyps, kernel.HypothesisInput{Statement: h.Statement, Supports: h.Supports, Confidence: h.Confidence, DisproveBy: h.DisproveBy})
	}
	env, err := k.Plan(kernel.PlanInput{
		TaskID:         in.TaskID,
		Hypotheses:     hyps,
		ChangeBoundary: domain.ChangeBoundary{Files: in.Files, Symbols: in.Symbols, Reason: in.BoundaryReason},
		Verification:   in.Verification, Uncertainty: in.Uncertainty,
		TimeoutOverrides: in.TimeoutOverrides,
	})
	return result(env, err)
}

func (s *Server) handleBeginChange(_ context.Context, _ *sdkmcp.CallToolRequest, in beginChangeInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	var ttl time.Duration
	if in.TTL != "" {
		ttl, err = time.ParseDuration(in.TTL)
		if err != nil {
			return envelopeErrorResult(fmt.Errorf("invalid change lease ttl: %w", err))
		}
	}
	env, err := k.BeginChange(kernel.BeginChangeInput{TaskID: in.TaskID, Actor: in.Actor, TTL: ttl})
	return result(env, err)
}

func (s *Server) handleVerify(ctx context.Context, _ *sdkmcp.CallToolRequest, in verifyInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	claimSpecs := make([]domain.VerificationClaim, 0, len(in.ClaimSpecs))
	for _, claim := range in.ClaimSpecs {
		claimSpecs = append(claimSpecs, domain.VerificationClaim{
			ID: claim.ID, Statement: claim.Statement, Surface: domain.Surface(claim.Surface),
			Verifier: claim.Verifier, Contract: claim.Contract, Required: true,
		})
	}
	env, err := k.Verify(ctx, kernel.VerifyInput{
		TaskID: in.TaskID, Actor: in.Actor, Claims: in.Claims, ChangedFiles: in.ChangedFiles,
		ClaimSpecs:  claimSpecs,
		BrowserSpec: in.BrowserSpec, TerminalSpec: in.TerminalSpec,
		ArtifactRef: in.ArtifactRef, SecretProject: in.SecretProject,
		DisableAutoSpecs: in.DisableAutoSpecs,
		NoOpAcknowledged: in.NoOpAcknowledged,
	})
	return result(env, err)
}

func (s *Server) handleRemember(ctx context.Context, _ *sdkmcp.CallToolRequest, in rememberInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.Remember(ctx, kernel.RememberInput{
		TaskID: in.TaskID, Outcome: in.Outcome, Importance: in.Importance,
		Tags: in.Tags, VerificationNotPossible: in.VerificationNotPossible,
		AcceptFailed: in.AcceptFailed,
	})
	return result(env, err)
}

func (s *Server) handleListTasks(_ context.Context, _ *sdkmcp.CallToolRequest, in listTasksInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	tasks, err := k.ListTasks()
	if err != nil {
		return result(nil, err)
	}
	return result(tasks, nil)
}

func (s *Server) handleSessions(_ context.Context, _ *sdkmcp.CallToolRequest, in sessionsInput) (*sdkmcp.CallToolResult, any, error) {
	// Workspace-independent: reads the global state tree, so no kernel is built.
	sessions, err := kernel.AllSessions(kernel.SessionFilter{Repo: in.Repo, ActiveOnly: in.Active, Query: in.Query})
	return result(sessions, err)
}

func (s *Server) handleTimeline(_ context.Context, _ *sdkmcp.CallToolRequest, in timelineInput) (*sdkmcp.CallToolResult, any, error) {
	// Central sessions are located by task ID; workspace covers local overrides.
	entries, err := kernel.TimelineIn(in.Workspace, in.TaskID)
	return result(entries, err)
}

func (s *Server) handleMetrics(_ context.Context, _ *sdkmcp.CallToolRequest, in metricsInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	if in.TaskID != "" {
		m, err := k.TaskMetrics(in.TaskID)
		return result(m, err)
	}
	wm, per, err := k.WorkspaceMetrics()
	if err != nil {
		return result(nil, err)
	}
	return result(map[string]any{"workspace": wm, "tasks": per}, nil)
}

func (s *Server) handleOverview(_ context.Context, _ *sdkmcp.CallToolRequest, in overviewInput) (*sdkmcp.CallToolResult, any, error) {
	// Workspace-independent: aggregates the whole central sessions tree.
	hours := in.StaleAfterHours
	if hours <= 0 {
		hours = 24
	}
	o, err := kernel.BuildOverview(time.Duration(hours)*time.Hour, time.Now())
	return result(o, err)
}

func (s *Server) handleArchive(_ context.Context, _ *sdkmcp.CallToolRequest, in archiveInput) (*sdkmcp.CallToolResult, any, error) {
	// Workspace-independent: the session is located by task ID across the tree.
	slug, err := kernel.ArchiveSession(in.TaskID)
	if err != nil {
		return result(nil, err)
	}
	return result(map[string]string{"archived": in.TaskID, "repo": slug}, nil)
}

func (s *Server) handleUnarchive(_ context.Context, _ *sdkmcp.CallToolRequest, in unarchiveInput) (*sdkmcp.CallToolResult, any, error) {
	// Workspace-independent: the session is located by task ID across the tree.
	slug, err := kernel.UnarchiveSession(in.TaskID)
	if err != nil {
		return result(nil, err)
	}
	return result(map[string]string{"unarchived": in.TaskID, "repo": slug}, nil)
}

func (s *Server) handleStatus(ctx context.Context, _ *sdkmcp.CallToolRequest, in statusInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	rep, err := k.Status(ctx, in.TaskID, in.Detail)
	return result(rep, err)
}

func (s *Server) handleResolve(_ context.Context, _ *sdkmcp.CallToolRequest, in resolveInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.Resolve(kernel.ResolveInput{
		TaskID: in.TaskID, HypothesisID: in.HypothesisID, Status: in.Status,
		Reason: in.Reason, Evidence: in.Evidence,
	})
	return result(env, err)
}

func (s *Server) handleObservation(_ context.Context, _ *sdkmcp.CallToolRequest, in observationInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.RecordObservation(kernel.ObservationInput{
		TaskID: in.TaskID, Claim: in.Claim, Category: in.Category, Origin: in.Origin,
		Actor: in.Actor, URI: in.URI, Confidence: in.Confidence, Sensitive: in.Sensitive,
	})
	return result(env, err)
}

func (s *Server) handleRequestDecision(_ context.Context, _ *sdkmcp.CallToolRequest, in requestDecisionInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	options := make([]domain.DecisionOption, 0, len(in.Options))
	for _, option := range in.Options {
		options = append(options, domain.DecisionOption{ID: option.ID, Label: option.Label, Consequence: option.Consequence})
	}
	env, err := k.RequestDecision(kernel.RequestDecisionInput{
		TaskID: in.TaskID, Question: in.Question, Options: options, Requester: in.Requester,
	})
	return result(env, err)
}

func (s *Server) handleAnswerDecision(_ context.Context, _ *sdkmcp.CallToolRequest, in answerDecisionInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	if in.Resume {
		env, err := k.ResumeDecision(in.TaskID)
		return result(env, err)
	}
	env, err := k.AnswerDecision(kernel.AnswerDecisionInput{
		TaskID: in.TaskID, DecisionID: in.DecisionID, Answer: in.Answer, Responder: in.Responder,
	})
	return result(env, err)
}

func (s *Server) handleHandoff(_ context.Context, _ *sdkmcp.CallToolRequest, in handoffInput) (*sdkmcp.CallToolResult, any, error) {
	handoff, err := kernel.BuildHandoffIn(in.Workspace, in.TaskID, time.Now())
	return result(handoff, err)
}

func (s *Server) handleAbort(_ context.Context, _ *sdkmcp.CallToolRequest, in abortInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.AbortTask(in.TaskID, in.Reason)
	return result(env, err)
}

func (s *Server) handleReadEvidence(_ context.Context, _ *sdkmcp.CallToolRequest, in readEvidenceInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	ev, err := k.ReadEvidence(in.TaskID, in.EvidenceID)
	if err != nil {
		return result(nil, err)
	}
	return result(ev, nil)
}

func (s *Server) handleRecallCases(ctx context.Context, _ *sdkmcp.CallToolRequest, in recallCasesInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return envelopeErrorResult(err)
	}
	env, err := k.RecallCasesEnvelope(ctx, in.Query, in.Repo, in.Limit)
	return result(env, err)
}
func (s *Server) handleReadArtifact(ctx context.Context, _ *sdkmcp.CallToolRequest, in readArtifactInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	preview, err := k.PreviewArtifactWithOptions(ctx, in.TaskID, in.Ref, in.Path, in.MaxBytes, in.AllowBinary)
	if err != nil {
		return result(nil, err)
	}
	return result(preview, nil)
}

// ---- helpers ----

func toSurfaces(ss []string) []domain.Surface {
	out := make([]domain.Surface, 0, len(ss))
	for _, s := range ss {
		out = append(out, domain.Surface(s))
	}
	return out
}

func toAcceptanceCriteria(criteria []acceptanceCriterionArg) []domain.AcceptanceCriterion {
	out := make([]domain.AcceptanceCriterion, 0, len(criteria))
	for _, criterion := range criteria {
		out = append(out, domain.AcceptanceCriterion{ID: criterion.ID, Statement: criterion.Statement})
	}
	return out
}

// envelopeErrorResult preserves the shared lifecycle result contract when a
// failure occurs before the kernel can return its own envelope. In particular,
// clients that consume only structuredContent still receive the same error as
// clients that read the JSON text compatibility block.
func envelopeErrorResult(err error) (*sdkmcp.CallToolResult, any, error) {
	message := err.Error()
	return result(domain.Envelope{Summary: message, Error: message}, err)
}

func result(v any, err error) (*sdkmcp.CallToolResult, any, error) {
	structured, ok, nonzero := envelopeResultState(v)
	if err != nil && (!structured || !nonzero) {
		return errResult(err.Error()), nil, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if mErr := enc.Encode(v); mErr != nil {
		return errResult(mErr.Error()), nil, nil
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(bytes.TrimRight(buf.Bytes(), "\n"))}},
		IsError: err != nil || (structured && !ok),
	}, v, nil
}

// envelopeResultState recognizes the shared lifecycle envelope, including
// reports that embed it. Kernel rule rejections are valid tool responses with
// useful recovery context, but MCP still needs IsError=true so clients that do
// not inspect the nested `ok` field cannot mistake a rejected mutation for a
// success. nonzero lets result preserve a populated envelope alongside a Go
// error while retaining the concise plain-text fallback for tools whose
// documented result is not the shared lifecycle envelope.
func envelopeResultState(v any) (structured, ok, nonzero bool) {
	switch value := v.(type) {
	case domain.Envelope:
		return true, value.OK, envelopeNonzero(value)
	case *domain.Envelope:
		if value == nil {
			return true, false, false
		}
		return true, value.OK, envelopeNonzero(*value)
	case kernel.StatusReport:
		return true, value.OK, envelopeNonzero(value.Envelope)
	case *kernel.StatusReport:
		if value == nil {
			return true, false, false
		}
		return true, value.OK, envelopeNonzero(value.Envelope)
	default:
		return false, false, false
	}
}

func envelopeNonzero(value domain.Envelope) bool {
	return value.OK || value.TaskID != "" || value.Phase != "" || value.Summary != "" || value.Error != "" ||
		len(value.Facts) > 0 || len(value.Hypotheses) > 0 || len(value.Warnings) > 0 ||
		len(value.NextActions) > 0 || len(value.Actions) > 0 || len(value.Artifacts) > 0
}

func errResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "Error: " + msg}},
		IsError: true,
	}
}
