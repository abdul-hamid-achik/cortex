// Package kernel is Cortex's shared service layer: the phase machine, routing,
// verification policy, and scope control that turn stateless tool calls into an
// evidence-driven reasoning loop (SPEC §3.1). Both the CLI and the MCP server
// are thin front-ends over this package — never put business logic in those
// layers (the ecosystem's cmd → service → adapters rule).
package kernel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// Kernel owns one workspace's case store and adapter registry.
type Kernel struct {
	cfg      config.Config
	store    *casefs.Store
	reg      *adapters.Registry
	git      *adapters.Git
	red      *redact.Redactor
	approver Approver
	now      func() time.Time
	recaller caseRecaller // cross-case disproof recall surface (veclite); nil when absent
}

// New builds a kernel for a workspace with a default adapter registry (git,
// codemap, vecgrep, cairntrace, glyphrun, fcheap, tvault).
func New(cfg config.Config) (*Kernel, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cortex configuration: %w", err)
	}
	store, err := casefs.New(cfg.CasesDir)
	if err != nil {
		return nil, err
	}
	// Cortex's own state must never register as a workspace change. Ignore the
	// cases parent dir when cases are repo-local; no-op for the XDG default (out
	// of the tree). Shared with the eval harness (config.EnsureStateIgnored).
	config.EnsureStateIgnored(cfg.Workspace, cfg.CasesDir)
	git := adapters.NewGit()
	reg := adapters.NewRegistry(
		git,
		adapters.NewCodemap(),
		adapters.NewVecgrep(),
		adapters.NewCairntrace(),
		adapters.NewGlyphrun(),
		adapters.NewFcheap(),
		adapters.NewVidtrace(),
		adapters.NewTvault(),
		adapters.NewVeclite(),
		adapters.NewCommandVerifier(commandSpecs(cfg.Verifiers)),
	)
	reg.SetMaxParallel(cfg.Budget.MaxParallelCalls)         // SPEC §7.3
	reg.SetMaxAutoRetries(cfg.Budget.MaxAutoRetriesPerTool) // SPEC §17.3
	k := &Kernel{cfg: cfg, store: store, reg: reg, git: git, red: redact.New(cfg.RedactLiterals...), now: time.Now}
	if vl, ok := reg.Get("veclite").(*adapters.Veclite); ok {
		vl.Configure(adapters.VecliteConfig{
			DBPath:     cfg.Recall.DBPath,
			EmbedModel: cfg.Recall.EmbedModel,
			EmbedURL:   cfg.Recall.EmbedURL,
			Enabled:    cfg.Recall.Enabled,
		})
		k.recaller = vl // cross-case recall surface (SPEC §15.4)
	}
	// SPEC §16.2 #4: wire an env-gated approver so a harness/CI can allow
	// external mutations without code changes. Default (unset) keeps the
	// built-in deny — external actions stay blocked until explicitly approved.
	if approveExternal() {
		k.SetApprover(envApprover{})
	}
	return k, nil
}

func commandSpecs(configured map[string]config.CommandVerifier) map[string]adapters.CommandSpec {
	out := make(map[string]adapters.CommandSpec, len(configured))
	for name, v := range configured {
		out[name] = adapters.CommandSpec{
			Argv: append([]string(nil), v.Argv...), Kind: string(v.Kind),
			Surface: string(v.Surface), Timeout: v.Timeout,
		}
	}
	return out
}

// envApprover approves external-mutation and secreted-execution actions when
// CORTEX_APPROVE_EXTERNAL is set (truthy: 1, true, yes). It never weakens
// read-only or local-mutation classes (those are always allowed). The action is
// still recorded in the audit trail by run() — approval is not silent.
type envApprover struct{}

func (envApprover) Approve(_, _, _ string, class domain.ActionClass) bool {
	switch class {
	case domain.ActionConfiguredExecution:
		return approveCommands()
	case domain.ActionExternalMutation, domain.ActionSecretedExecution:
		return approveExternal()
	default:
		return false
	}
}

// approveExternal reads CORTEX_APPROVE_EXTERNAL; truthy values enable the env
// approver. Unset or any other value keeps the default deny.
func approveExternal() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CORTEX_APPROVE_EXTERNAL"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// approveCommands is a trusted-process boundary for repository-configured
// argv. A project cortex.yaml cannot grant this permission to itself; the
// person or harness launching Cortex must set it out of band.
func approveCommands() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CORTEX_APPROVE_COMMANDS"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// NewWith builds a kernel with an explicit registry (used by tests).
func NewWith(cfg config.Config, store *casefs.Store, reg *adapters.Registry) *Kernel {
	k := &Kernel{cfg: cfg, store: store, reg: reg, red: redact.New(cfg.RedactLiterals...), now: time.Now}
	if g, ok := reg.Get("git").(*adapters.Git); ok {
		k.git = g
	}
	if vl, ok := reg.Get("veclite").(*adapters.Veclite); ok {
		vl.Configure(adapters.VecliteConfig{
			DBPath: cfg.Recall.DBPath, EmbedModel: cfg.Recall.EmbedModel,
			EmbedURL: cfg.Recall.EmbedURL, Enabled: cfg.Recall.Enabled,
		})
		k.recaller = vl
	}
	reg.SetMaxAutoRetries(cfg.Budget.MaxAutoRetriesPerTool) // SPEC §17.3
	if approveExternal() {
		k.SetApprover(envApprover{})
	}
	return k
}

// Store exposes the case store (for read-only CLI/MCP helpers).
func (k *Kernel) Store() *casefs.Store { return k.store }

// Registry exposes the adapter registry (for health reporting).
func (k *Kernel) Registry() *adapters.Registry { return k.reg }

// transition updates an in-memory case to a new phase, enforcing the structural
// graph (SPEC §6.2). Data-precondition invariants are checked by the caller.
// The caller records phase history only after the corresponding case snapshot
// commits; appending first can leave a phantom transition after a failed CAS.
func (k *Kernel) transition(c *domain.CaseFile, to domain.Phase) error {
	if c.Status == to {
		return nil
	}
	if !domain.CanTransition(c.Status, to) {
		return domain.ErrIllegalTransition{From: c.Status, To: to}
	}
	c.Status = to
	return nil
}

// recordPhase appends a phase transition to the case's history (phases.jsonl) so
// the reasoning loop leaves a durable, timestamped trail — the source for
// `cortex timeline` and phase-latency metrics. Best effort: a ledger write
// failure never blocks the transition itself.
func (k *Kernel) recordPhase(taskID string, from, to domain.Phase) {
	_ = k.store.AppendPhaseEvent(taskID, casefs.PhaseEvent{Timestamp: k.now().UTC(), From: from, To: to})
}

// stampEvidence promotes an adapter Fact into a durable Evidence record whose
// RawRef self-references the record (used when no tool raw output is available).
func (k *Kernel) stampEvidence(taskID string, tool string, f adapters.Fact) (domain.Evidence, error) {
	return k.stampEvidenceRaw(taskID, tool, f, "")
}

func (k *Kernel) stampEvidenceOnce(taskID, stableID, tool string, f adapters.Fact, timestamp time.Time) (domain.Evidence, error) {
	ev := k.buildEvidenceDerived(taskID, tool, f, "", nil)
	ev.ID = stableID
	ev.Timestamp = timestamp.UTC()
	ev.RawRef = fmt.Sprintf("case://%s/evidence/%s", taskID, stableID)
	durable, _, err := k.store.AppendEvidenceOnce(taskID, ev)
	return durable, err
}

// storeRaw persists a tool result's redacted raw output once and returns a
// resolvable rawRef (case://<taskID>/raw/<id>), or "" when there is no raw. The
// raw is redacted again defensively before it touches disk (SPEC §10.4).
func (k *Kernel) storeRaw(taskID string, res adapters.Result) string {
	if res.Raw == "" {
		return ""
	}
	rawID := ids.New("raw")
	// Apply the per-tool raw cap (SPEC §7.3 max_raw_output_bytes_per_tool) here,
	// at the storage boundary — NOT on the string the adapter parses, which would
	// corrupt valid-but-large JSON. The cap bounds only what is kept on disk.
	raw := capRawForStore(k.red.String(res.Raw), k.cfg.Budget.MaxRawOutputBytesPerTool)
	if err := k.store.WriteRaw(taskID, rawID, raw); err != nil {
		return ""
	}
	return fmt.Sprintf("case://%s/raw/%s", taskID, rawID)
}

// capRawForStore truncates raw tool output to the configured byte budget before
// it is written to the case file, appending a visible marker. A cap < 1 (unset)
// means "do not truncate".
func capRawForStore(s string, max int) string {
	if max > 0 && len(s) > max {
		bounded, _ := boundedUTF8(s, max)
		return bounded + "\n…(truncated)"
	}
	return s
}

// stampEvidenceDerived promotes a Fact into a durable Evidence record carrying
// causal-routing provenance: derivedFrom names the discovery evidence whose
// candidate produced this structural claim. Empty derivedFrom is a plain stamp.
func (k *Kernel) stampEvidenceDerived(taskID, tool string, f adapters.Fact, rawRef string, derivedFrom []string) (domain.Evidence, error) {
	ev := k.buildEvidenceDerived(taskID, tool, f, rawRef, derivedFrom)
	if err := k.store.AppendEvidence(taskID, ev); err != nil {
		return domain.Evidence{}, err
	}
	return ev, nil
}

// buildEvidenceDerived creates the exact redacted durable record without
// writing it. Verify uses this to stage facts until its case/lease CAS wins;
// ordinary investigation and note paths call stampEvidenceDerived above.
func (k *Kernel) buildEvidenceDerived(taskID, tool string, f adapters.Fact, rawRef string, derivedFrom []string) domain.Evidence {
	id := ids.New("ev")
	// Enforce invariant #4 (SPEC §6.3): no secret value enters an evidence
	// record. Adapter facts are parsed from already-redacted tool output, but
	// human/model-supplied facts (e.g. cortex_resolve reasons) are NOT — so
	// redact here, at the write boundary, for EVERY source. Flag sensitivity
	// when the redactor matched something.
	claim := k.red.String(f.Claim)
	uri := k.red.String(f.URI)
	sens := f.Sensitive || k.red.Detected(f.Claim) || k.red.Detected(f.URI)
	if f.Location != nil {
		sens = sens || k.red.Detected(f.Location.File) || k.red.Detected(f.Location.Symbol)
	}
	ref := rawRef
	if ref == "" {
		ref = fmt.Sprintf("case://%s/evidence/%s", taskID, id)
	}
	ev := domain.Evidence{
		ID:          id,
		Timestamp:   k.now().UTC(),
		Kind:        mapKind(f.Kind),
		Source:      domain.Source{Tool: tool, URI: uri},
		Claim:       claim,
		Confidence:  mapConfidence(f.Confidence),
		Sensitivity: sensitivity(sens),
		RawRef:      ref,
		DerivedFrom: derivedFrom,
	}
	if f.Location != nil {
		ev.Location = &domain.Location{
			File: k.red.String(f.Location.File), StartLine: f.Location.StartLine,
			EndLine: f.Location.EndLine, Symbol: k.red.String(f.Location.Symbol),
		}
	}
	return ev
}

func (k *Kernel) stampEvidenceRaw(taskID string, tool string, f adapters.Fact, rawRef string) (domain.Evidence, error) {
	return k.stampEvidenceDerived(taskID, tool, f, rawRef, nil)
}

type commandActorContextKey struct{}

func withCommandActor(ctx context.Context, actor string) context.Context {
	if strings.TrimSpace(actor) == "" {
		return ctx
	}
	return context.WithValue(ctx, commandActorContextKey{}, actor)
}

func commandActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	actor, _ := ctx.Value(commandActorContextKey{}).(string)
	return strings.TrimSpace(actor)
}

func (k *Kernel) currentCommandActor(taskID string) string {
	actor := ""
	if taskID != "" {
		if c, err := k.store.Load(taskID); err == nil {
			actor = firstNonEmptyStr(activeLeaseActor(c, k.now().UTC()), c.Actor)
		}
	}
	return actor
}

// recordCommand writes a non-sensitive audit entry for a tool invocation,
// including its action class so the security posture is inspectable
// (SPEC §16.2 #7 records capability and result, not secret contents).
func (k *Kernel) recordCommand(taskID, tool, op string, class domain.ActionClass, status adapters.Status, started time.Time, note string) {
	k.recordCommandAs(k.currentCommandActor(taskID), taskID, tool, op, class, status, started, note)
}

func (k *Kernel) recordCommandAs(actor, taskID, tool, op string, class domain.ActionClass, status adapters.Status, started time.Time, note string) {
	_ = k.store.AppendCommand(taskID, casefs.CommandRecord{
		Timestamp:   k.now().UTC(),
		Actor:       actor,
		Tool:        tool,
		Operation:   op,
		ActionClass: string(class),
		Status:      string(status),
		DurationMs:  time.Since(started).Milliseconds(),
		Note:        k.red.String(note),
	})
}

// recordWrite audits a local-mutation write performed via a direct adapter
// method (fcheap stash, vecgrep memory, codemap annotate) so the audit trail is
// complete, not just the query path (SPEC §16.2 #7).
func (k *Kernel) recordWrite(taskID, tool, op string, err error) {
	k.recordWriteAs(k.currentCommandActor(taskID), taskID, tool, op, err)
}

func (k *Kernel) recordWriteAs(actor, taskID, tool, op string, err error) {
	status := adapters.StatusAuthoritative
	note := ""
	if err != nil {
		status = adapters.StatusError
		note = clipStr(err.Error(), 80)
	}
	k.recordCommandAs(actor, taskID, tool, op, domain.ActionLocalMutation, status, k.now(), note)
}

// Approver decides whether a mutation-class action may run. A harness injects
// one to gate external mutation / secret-backed execution — the SPEC §16.2 #4
// explicit approval integration point.
type Approver interface {
	Approve(taskID, tool, op string, class domain.ActionClass) bool
}

// SetApprover installs an approval hook (nil restores the built-in policy).
func (k *Kernel) SetApprover(a Approver) { k.approver = a }

// actionAllowed applies the action-class policy (SPEC §16.3): read-only and
// local-mutation run freely within an active task; external mutation and
// secret-backed execution require an explicit decision — from the injected
// approver, or the built-in default (deny external; allow secreted only when
// the tvault capability is present, since redaction is already enforced).
func (k *Kernel) actionAllowed(taskID, tool, op string, class domain.ActionClass) bool {
	switch class {
	case domain.ActionReadOnly, domain.ActionLocalMutation:
		return true
	case domain.ActionConfiguredExecution:
		if k.approver != nil {
			return k.approver.Approve(taskID, tool, op, class)
		}
		return approveCommands()
	case domain.ActionExternalMutation, domain.ActionSecretedExecution:
		if k.approver != nil {
			return k.approver.Approve(taskID, tool, op, class)
		}
		if class == domain.ActionSecretedExecution {
			return k.reg.Get("tvault") != nil
		}
		return false // external mutation requires explicit approval
	default:
		return true
	}
}

// run executes an adapter operation, gates it by action class, records the
// audit entry, and returns the normalized result. A missing adapter degrades to
// an unavailable result; a policy-blocked action never touches the adapter.
func (k *Kernel) run(ctx context.Context, tool string, req adapters.Request) adapters.Result {
	a := k.reg.Get(tool)
	if a == nil {
		return adapters.Result{Tool: tool, Operation: req.Operation, Status: adapters.StatusUnavailable,
			Summary: tool + " is not registered"}
	}
	class := domain.ClassifyOp(tool, req.Operation)
	started := k.now()
	actor := commandActorFromContext(ctx)
	if actor == "" {
		actor = k.currentCommandActor(req.TaskID)
	}
	if !k.actionAllowed(req.TaskID, tool, req.Operation, class) {
		note := fmt.Sprintf("%s.%s (%s) blocked: requires explicit approval (SPEC §16.2)", tool, req.Operation, class)
		k.recordCommandAs(actor, req.TaskID, tool, req.Operation, class, adapters.StatusBlocked, started, note)
		return adapters.Result{Tool: tool, Operation: req.Operation, Status: adapters.StatusBlocked, Summary: note,
			Warnings: []string{note}}
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	if req.Input["dir"] == nil {
		req.Input["dir"] = k.cfg.Workspace
	}
	// Apply a per-task timeout override if the case declares one for this tool
	// (SPEC §17.2). The override bounds the context; the adapter's own timeout
	// is the min of its default and this deadline.
	if req.TaskID != "" {
		if c, err := k.store.Load(req.TaskID); err == nil {
			if d, ok := c.TimeoutOverrides[tool]; ok {
				if dur, perr := time.ParseDuration(d); perr == nil && dur > 0 {
					cctx, cancel := context.WithTimeout(ctx, dur)
					defer cancel()
					ctx = cctx
				}
			}
		}
	}
	res, err := a.Execute(ctx, req)
	if err != nil {
		res = adapters.Result{Tool: tool, Operation: req.Operation, Status: adapters.StatusError, Summary: err.Error()}
	}
	k.recordCommandAs(actor, req.TaskID, tool, req.Operation, class, res.Status, started, clipStr(res.Summary, 120))
	return res
}

// --- envelope helpers ---

// envelope builds the shared result envelope from a case plus fresh facts.
func (k *Kernel) envelope(c *domain.CaseFile, summary string, facts []domain.Evidence, warnings, next []string) domain.Envelope {
	env := domain.Envelope{
		OK:           true,
		TaskID:       c.ID,
		Phase:        c.Status,
		Summary:      k.red.String(summary),
		Warnings:     k.redactStrings(warnings),
		NextActions:  k.redactStrings(next),
		RawAvailable: hasRawEvidence(facts),
	}
	k.attachStructuredActions(&env, c)
	for _, f := range facts {
		env.Facts = append(env.Facts, domain.ToFactView(f))
	}
	return env
}

func hasRawEvidence(facts []domain.Evidence) bool {
	for _, fact := range facts {
		if strings.Contains(fact.RawRef, "/raw/") {
			return true
		}
	}
	return false
}

func (k *Kernel) redactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, k.red.String(value))
	}
	return out
}

func errEnvelope(taskID, msg string) domain.Envelope {
	return domain.Envelope{OK: false, TaskID: taskID, Summary: msg, Error: msg}
}

// --- mapping helpers ---

func mapKind(s string) domain.EvidenceKind {
	switch s {
	case "code_location":
		return domain.KindCodeLocation
	case "code_graph":
		return domain.KindCodeGraph
	case "semantic_search":
		return domain.KindSemanticSearch
	case "browser_run":
		return domain.KindBrowserRun
	case "terminal_run":
		return domain.KindTerminalRun
	case "unit_test":
		return domain.KindUnitTest
	case "build":
		return domain.KindBuild
	case "lint":
		return domain.KindLint
	case "artifact":
		return domain.KindArtifact
	case "human_report":
		return domain.KindHumanReport
	case "tool_unavailable":
		return domain.KindToolUnavailable
	default:
		return domain.KindModelInference
	}
}

func mapConfidence(s string) domain.Confidence {
	switch s {
	case "high":
		return domain.ConfidenceHigh
	case "medium":
		return domain.ConfidenceMedium
	case "low":
		return domain.ConfidenceLow
	default:
		return domain.ConfidenceUnknown
	}
}

func sensitivity(s bool) domain.Sensitivity {
	if s {
		return domain.SensitivitySensitive
	}
	return domain.SensitivityNormal
}

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
