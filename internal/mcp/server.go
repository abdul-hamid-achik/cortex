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

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/abdul-hamid-achik/cortex/internal/version"
)

const instructions = `Cortex is an evidence-guided agent kernel. It gives non-trivial engineering
work a durable case file, disciplined tool routing, bounded changes, and verification tied to
user-visible behavior. Use the six cognitive actions instead of coordinating raw tools by hand:

1. cortex_start_task — open a case for a goal; orients on git identity + tool health.
2. cortex_investigate — route a question through discovery (vecgrep) then structure (codemap);
   returns evidence IDs. Treat search output as candidates, NOT proof.
3. cortex_plan — before editing, state a testable hypothesis WITH a disproof path, a change
   boundary (files/symbols you expect to touch), and a verification plan. Plans without a
   disproof path are rejected.
4. cortex_verify — after editing, run the required verifiers (codemap review + browser/terminal
   specs), detect scope drift, and get a receipt per claim. A claim with no verifier is recorded
   not_run — never treated as passed.
5. cortex_remember — persist the outcome + uncertainty. A task cannot complete without a
   verification receipt or an explicit statement that verification was not possible.
6. cortex_status — phase, unresolved hypotheses, scope drift, missing verification, tool health.

As evidence accumulates, use cortex_resolve to mark a hypothesis confirmed/challenged/rejected —
history is kept; a rejected hypothesis records WHY, so contradicting evidence never silently
overwrites a prior explanation.

Also: cortex_abort_task (stop without deleting evidence) and cortex_read_evidence (full record
by ID). Never request or expose secret values — Cortex checks capability only.`

// Server wraps the go-sdk MCP server. Kernels are built per-call so one server
// process can serve tasks in any workspace the tools name.
type Server struct {
	defaultWorkspace string
	srv              *sdkmcp.Server
}

// NewServer builds an MCP server defaulting to the given workspace directory.
func NewServer(defaultWorkspace string) *Server {
	s := &Server{defaultWorkspace: defaultWorkspace}
	s.srv = sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "cortex", Version: version.Version},
		&sdkmcp.ServerOptions{Instructions: instructions},
	)
	s.register()
	return s
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
	Goal      string   `json:"goal" jsonschema:"the engineering goal for this task"`
	Workspace string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
	Mode      string   `json:"mode,omitempty" jsonschema:"change | investigate | review (default change)"`
	Surfaces  []string `json:"surfaces,omitempty" jsonschema:"user-visible surfaces: code, browser, terminal, artifact, secret"`
	Risk      string   `json:"risk,omitempty" jsonschema:"low | medium | high (default medium)"`
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
	TaskID         string          `json:"taskId" jsonschema:"the task to plan"`
	Hypotheses     []hypothesisArg `json:"hypotheses" jsonschema:"one or more hypotheses, each with a disproof path"`
	Files          []string        `json:"files,omitempty" jsonschema:"files you expect to change (the change boundary)"`
	Symbols        []string        `json:"symbols,omitempty" jsonschema:"symbols you expect to change"`
	BoundaryReason string          `json:"boundaryReason,omitempty" jsonschema:"why these files/symbols are the expected change set"`
	Verification   []string        `json:"verification,omitempty" jsonschema:"required verifiers (e.g. codemap_review, cairntrace_flow)"`
	Uncertainty    string          `json:"uncertainty" jsonschema:"explicit statement of what remains uncertain (required)"`
	Workspace      string          `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type verifyInput struct {
	TaskID       string   `json:"taskId" jsonschema:"the task to verify"`
	Claims       []string `json:"claims,omitempty" jsonschema:"the user-facing claims to prove"`
	ChangedFiles []string `json:"changedFiles,omitempty" jsonschema:"changed files; derived from git when omitted"`
	BrowserSpec  string   `json:"browserSpec,omitempty" jsonschema:"cairntrace spec path to prove browser claims"`
	TerminalSpec string   `json:"terminalSpec,omitempty" jsonschema:"glyphrun spec path to prove terminal claims"`
	Workspace    string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

type rememberInput struct {
	TaskID                  string   `json:"taskId" jsonschema:"the task to complete"`
	Outcome                 string   `json:"outcome" jsonschema:"a concise, provenance-rich outcome summary"`
	Importance              float64  `json:"importance,omitempty" jsonschema:"0..1 importance for durable memory (default 0.5)"`
	Tags                    []string `json:"tags,omitempty" jsonschema:"tags for recall"`
	VerificationNotPossible bool     `json:"verificationNotPossible,omitempty" jsonschema:"set true to record explicitly that no verifier could run"`
	Workspace               string   `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
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
	TaskID    string `json:"taskId" jsonschema:"the task the artifact belongs to"`
	Ref       string `json:"ref" jsonschema:"an evidence rawRef or artifact reference (case://…/raw/… or fcheap://…)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"repository directory; defaults to the server working directory"`
}

func (s *Server) register() {
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_start_task",
		Description: "Create a case file for a non-trivial engineering task and perform lightweight orientation (git identity + tool health). Returns the task ID and the recommended next action.",
	}, s.handleStart)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_investigate",
		Description: "Route a question through discovery (vecgrep) then structure (codemap), record the returned evidence with provenance, and return a bounded summary. Search output is recorded as candidates, not proof.",
	}, s.handleInvestigate)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_plan",
		Description: "The planning gate. Store hypotheses (each REQUIRES a disproof path), a change boundary (files/symbols), and a verification plan. Rejects plans with no disproof path or (for change tasks) no boundary. Not a code generator.",
	}, s.handlePlan)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_verify",
		Description: "Run the verification policy after editing: structural diff review (codemap), any provided browser/terminal specs, and scope-drift detection. Returns a receipt per claim; a claim with no relevant verifier is recorded not_run, never passed.",
	}, s.handleVerify)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_remember",
		Description: "Persist a concise outcome to durable memory and complete the task. A task cannot complete without a verification receipt or an explicit verificationNotPossible acknowledgment.",
	}, s.handleRemember)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_status",
		Description: "Report a task's phase, unresolved hypotheses, scope drift, missing verification, and (with detail=full) tool health.",
	}, s.handleStatus)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_resolve",
		Description: "Update a hypothesis's status as evidence accumulates (confirmed/challenged/rejected). History is retained and the resolution is appended to the evidence ledger — this is how contradicting evidence is handled without silently overwriting a prior explanation.",
	}, s.handleResolve)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_abort_task",
		Description: "Stop the active task without deleting its evidence. Requires a reason.",
	}, s.handleAbort)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_read_evidence",
		Description: "Return a full evidence record by ID (raw detail is kept out of investigate/verify results to protect the context window). The record's rawRef points to the raw tool output — fetch it with cortex_read_artifact.",
	}, s.handleReadEvidence)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "cortex_read_artifact",
		Description: "Resolve an evidence rawRef (case://…/raw/…) to the raw tool output that backed it, or an fcheap:// stash reference to retrieval guidance. Use when a compact fact isn't enough and you need the underlying detail.",
	}, s.handleReadArtifact)
}

// ---- handlers (thin: build kernel, call kernel, return JSON) ----

func (s *Server) handleStart(ctx context.Context, _ *sdkmcp.CallToolRequest, in startInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	env, _ := k.StartTask(ctx, kernel.StartInput{
		Goal: in.Goal, Workspace: in.Workspace, Mode: domain.Mode(in.Mode),
		Surfaces: toSurfaces(in.Surfaces), Risk: in.Risk,
	})
	return result(env, nil)
}

func (s *Server) handleInvestigate(ctx context.Context, _ *sdkmcp.CallToolRequest, in investigateInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	env, _ := k.Investigate(ctx, kernel.InvestigateInput{
		TaskID: in.TaskID, Question: in.Question, Surfaces: toSurfaces(in.Surfaces), Depth: in.Depth, Video: in.Video,
	})
	return result(env, nil)
}

func (s *Server) handlePlan(_ context.Context, _ *sdkmcp.CallToolRequest, in planInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	hyps := make([]kernel.HypothesisInput, 0, len(in.Hypotheses))
	for _, h := range in.Hypotheses {
		hyps = append(hyps, kernel.HypothesisInput{Statement: h.Statement, Supports: h.Supports, Confidence: h.Confidence, DisproveBy: h.DisproveBy})
	}
	env, _ := k.Plan(kernel.PlanInput{
		TaskID: in.TaskID, Hypotheses: hyps,
		ChangeBoundary: domain.ChangeBoundary{Files: in.Files, Symbols: in.Symbols, Reason: in.BoundaryReason},
		Verification:   in.Verification, Uncertainty: in.Uncertainty,
	})
	return result(env, nil)
}

func (s *Server) handleVerify(ctx context.Context, _ *sdkmcp.CallToolRequest, in verifyInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	env, _ := k.Verify(ctx, kernel.VerifyInput{
		TaskID: in.TaskID, Claims: in.Claims, ChangedFiles: in.ChangedFiles,
		BrowserSpec: in.BrowserSpec, TerminalSpec: in.TerminalSpec,
	})
	return result(env, nil)
}

func (s *Server) handleRemember(ctx context.Context, _ *sdkmcp.CallToolRequest, in rememberInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	env, _ := k.Remember(ctx, kernel.RememberInput{
		TaskID: in.TaskID, Outcome: in.Outcome, Importance: in.Importance,
		Tags: in.Tags, VerificationNotPossible: in.VerificationNotPossible,
	})
	return result(env, nil)
}

func (s *Server) handleStatus(ctx context.Context, _ *sdkmcp.CallToolRequest, in statusInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	rep, _ := k.Status(ctx, in.TaskID, in.Detail)
	return result(rep, nil)
}

func (s *Server) handleResolve(_ context.Context, _ *sdkmcp.CallToolRequest, in resolveInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	env, _ := k.Resolve(kernel.ResolveInput{
		TaskID: in.TaskID, HypothesisID: in.HypothesisID, Status: in.Status,
		Reason: in.Reason, Evidence: in.Evidence,
	})
	return result(env, nil)
}

func (s *Server) handleAbort(_ context.Context, _ *sdkmcp.CallToolRequest, in abortInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	env, _ := k.AbortTask(in.TaskID, in.Reason)
	return result(env, nil)
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

func (s *Server) handleReadArtifact(_ context.Context, _ *sdkmcp.CallToolRequest, in readArtifactInput) (*sdkmcp.CallToolResult, any, error) {
	k, err := s.kernelFor(in.Workspace)
	if err != nil {
		return result(nil, err)
	}
	content, err := k.ReadArtifact(in.TaskID, in.Ref)
	if err != nil {
		return result(nil, err)
	}
	return result(map[string]string{"ref": in.Ref, "content": content}, nil)
}

// ---- helpers ----

func toSurfaces(ss []string) []domain.Surface {
	out := make([]domain.Surface, 0, len(ss))
	for _, s := range ss {
		out = append(out, domain.Surface(s))
	}
	return out
}

func result(v any, err error) (*sdkmcp.CallToolResult, any, error) {
	if err != nil {
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
	}, v, nil
}

func errResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "Error: " + msg}},
		IsError: true,
	}
}
