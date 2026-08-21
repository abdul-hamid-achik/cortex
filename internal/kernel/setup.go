package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// SetupStatus is the readiness state of one discovery/structure dependency for
// a workspace.
type SetupStatus string

const (
	// SetupReady means the tool is installed and has a usable index.
	SetupReady SetupStatus = "ready"
	// SetupStale means the tool is indexed and queryable, but the index has
	// drifted from the working tree. Structural/semantic queries may still run;
	// agents should refresh before treating results as authoritative.
	SetupStale SetupStatus = "stale"
	// SetupNeedsIndex means the tool is installed but has no usable index (absent
	// or broken, e.g. vecgrep's "embedding profile is missing").
	SetupNeedsIndex SetupStatus = "needs_index"
	// SetupMissing means the binary is not on PATH.
	SetupMissing SetupStatus = "missing"
	// SetupError means the tool is installed but the readiness probe failed
	// unexpectedly (including timeouts — slow ≠ unindexed).
	SetupError SetupStatus = "error"
)

// ToolSetup is the readiness of one specialist tool for this workspace.
type ToolSetup struct {
	Tool       string      `json:"tool"`
	Installed  bool        `json:"installed"`
	Status     SetupStatus `json:"status"`
	Detail     string      `json:"detail,omitempty"`
	FixCommand string      `json:"fixCommand,omitempty"`
}

// SetupReport is a read-only readiness snapshot for onboarding a workspace: is
// it a git repo, is Cortex configured, and are the discovery/structure tools
// installed and indexed. It never mutates anything and never runs indexing.
type SetupReport struct {
	Workspace     string      `json:"workspace"`
	IsRepo        bool        `json:"isRepo"`
	HasConfig     bool        `json:"hasConfig"`
	VerifierCount int         `json:"verifierCount"`
	Tools         []ToolSetup `json:"tools"`
}

// setupProbe describes how to check one tool's index readiness: which read-only
// search operation to run and the command that fixes a missing index.
type setupProbe struct {
	tool string
	op   string
	fix  string
}

var setupProbes = []setupProbe{
	{tool: "codemap", op: "status", fix: "codemap index"},
	{tool: "vecgrep", op: "status", fix: "vecgrep init && vecgrep index"},
}

// Setup probes the workspace's setup prerequisites. It is read-only: it checks
// git identity, config presence, and each discovery tool's install+index state,
// returning the exact command to fix each gap. It deliberately never runs
// indexing itself — indexing can be long-running and (for vecgrep) needs a
// local embedding service — so Cortex reports status and the fix rather than
// kicking off heavy external work unbidden.
func (k *Kernel) Setup(ctx context.Context) SetupReport {
	rep := SetupReport{
		Workspace:     k.cfg.Workspace,
		VerifierCount: len(k.cfg.Verifiers),
		HasConfig:     hasProjectConfig(k.cfg.Workspace),
		Tools:         []ToolSetup{},
	}
	if info, err := k.git.Status(ctx, k.cfg.Workspace); err == nil {
		rep.IsRepo = info.IsRepo
	}
	for _, p := range setupProbes {
		rep.Tools = append(rep.Tools, k.probeSetupTool(ctx, p))
	}
	return rep
}

// probeSetupTool checks one tool's install + index readiness via its native
// status operation (cheap). A search probe is deliberately avoided: vecgrep
// search can hit a slow embedder path, and a dummy query is a poor readiness
// signal while specialists are being optimized for latency.
func (k *Kernel) probeSetupTool(ctx context.Context, p setupProbe) ToolSetup {
	ts := ToolSetup{Tool: p.tool, FixCommand: p.fix}
	a := k.reg.Get(p.tool)
	if a == nil || a.Health(ctx) != nil {
		ts.Status = SetupMissing
		ts.Detail = "not on PATH"
		return ts
	}
	ts.Installed = true
	res, err := a.Execute(ctx, adapters.Request{Operation: p.op, Input: map[string]any{
		"dir": k.cfg.Workspace,
	}})
	if err != nil {
		ts.Status = SetupError
		ts.Detail = err.Error()
		return ts
	}
	if fix := resultFixCommand(res); fix != "" {
		ts.FixCommand = fix
	}
	if res.Status == adapters.StatusAuthoritative {
		ts.Status = SetupReady
		return ts
	}
	if adapters.IsTimeoutResult(res) {
		ts.Status = SetupError
		ts.Detail = res.Summary
		ts.FixCommand = "" // timeout is not fixed by indexing
		return ts
	}
	if res.Status == adapters.StatusPartial {
		// Indexed but stale remains queryable. Do not collapse this into
		// needs_index — that pushed agents to reindex before any structural
		// review on large repos that are merely drifted (graphite dogfood).
		ts.Status = SetupStale
		ts.Detail = res.Summary
		return ts
	}
	if res.Status == adapters.StatusError {
		ts.Status = SetupError
		ts.Detail = res.Summary
		return ts
	}
	ts.Status = SetupNeedsIndex
	ts.Detail = res.Summary
	return ts
}

func resultFixCommand(res adapters.Result) string {
	if adapters.IsTimeoutResult(res) {
		return ""
	}
	for _, f := range res.Facts {
		if fix := strings.TrimSpace(f.Attributes["fix"]); fix != "" {
			return fix
		}
	}
	return ""
}

// hasProjectConfig reports whether a project-level cortex.yaml/yml exists in
// the workspace (the global config is not counted — `cortex init` manages
// project configuration).
func hasProjectConfig(workspace string) bool {
	for _, name := range []string{
		filepath.Join(".config", "cortex.yaml"),
		"cortex.yml",
		"cortex.yaml",
	} {
		if fi, err := os.Stat(filepath.Join(workspace, name)); err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// setupGaps lists discovery/structure tools that are installed-but-unusable or
// missing, with the exact command that fixes each gap. Stale indexes are
// queryable — they warn via setupStaleWarnings, not as blocking gaps.
func setupGaps(rep SetupReport) []ToolSetup {
	var out []ToolSetup
	for _, ts := range rep.Tools {
		if ts.Status != SetupNeedsIndex && ts.Status != SetupError {
			continue
		}
		out = append(out, ts)
	}
	return out
}

func setupStaleWarnings(rep SetupReport) []string {
	var out []string
	for _, ts := range rep.Tools {
		if ts.Status != SetupStale {
			continue
		}
		detail := strings.TrimSpace(ts.Detail)
		if detail == "" {
			detail = "index has drifted from the working tree"
		}
		msg := fmt.Sprintf("%s discovery is stale — %s (still queryable; refresh before treating structure as authoritative)", ts.Tool, detail)
		if ts.FixCommand != "" {
			msg += "; optional: " + ts.FixCommand
		}
		out = append(out, msg)
	}
	return out
}

func discoveryGapsFromInvestigate(results []adapters.Result, facts []domain.Evidence) []ToolSetup {
	seen := map[string]bool{}
	var gaps []ToolSetup
	add := func(tool, detail, fix string, status SetupStatus) {
		if tool != "vecgrep" && tool != "codemap" {
			return
		}
		if seen[tool] {
			return
		}
		seen[tool] = true
		if strings.TrimSpace(fix) == "" && status == SetupNeedsIndex {
			fix = setupFixCommand(tool)
		}
		gaps = append(gaps, ToolSetup{
			Tool: tool, Installed: true, Status: status, Detail: detail,
			FixCommand: fix,
		})
	}
	for _, res := range results {
		switch res.Status {
		case adapters.StatusUnavailable, adapters.StatusError, adapters.StatusPartial:
			if adapters.IsTimeoutResult(res) {
				// Slow specialist ≠ missing index — surface as error, no index fix.
				add(res.Tool, res.Summary+"; prefer git grep / path-scoped reads this turn", "", SetupError)
				continue
			}
			if res.Status == adapters.StatusPartial && looksLikeStaleIndex(res.Summary) {
				// Usable with caution — not a needs_index gap.
				continue
			}
			if looksLikeSchemaOrProbeFailure(res.Summary, res.Warnings) {
				add(res.Tool, res.Summary, "", SetupError)
				continue
			}
			st := SetupNeedsIndex
			if res.Status == adapters.StatusError {
				st = SetupError
			}
			add(res.Tool, res.Summary, resultFixCommand(res), st)
		}
	}
	for _, ev := range facts {
		if ev.Kind != domain.KindToolUnavailable {
			continue
		}
		claim := strings.ToLower(ev.Claim)
		if !strings.Contains(claim, "not_indexed") && !strings.Contains(claim, "index") &&
			!strings.Contains(claim, "not registered") && !strings.Contains(claim, "collection not found") {
			continue
		}
		add(ev.Source.Tool, ev.Claim, "", SetupNeedsIndex)
	}
	return gaps
}

func setupFixCommand(tool string) string {
	for _, p := range setupProbes {
		if p.tool == tool {
			return p.fix
		}
	}
	return ""
}

func looksLikeStaleIndex(summary string) bool {
	s := strings.ToLower(summary)
	return strings.Contains(s, "stale") || strings.Contains(s, "changed file")
}

func looksLikeSchemaOrProbeFailure(summary string, warnings []string) bool {
	blob := strings.ToLower(summary + " " + strings.Join(warnings, " "))
	for _, needle := range []string{
		"incompatible schema", "schema_version", "invalid level", "invalid score",
		"parse", "decode", "unmarshal",
	} {
		if strings.Contains(blob, needle) {
			return true
		}
	}
	return false
}

// discoveryIndexUsable reports whether a specialist can still answer queries.
// Ready and stale both count — only missing/needs_index/error force the
// git-grep floor.
func discoveryIndexUsable(status SetupStatus) bool {
	return status == SetupReady || status == SetupStale
}

func setupGapWarnings(gaps []ToolSetup) []string {
	var out []string
	for _, ts := range gaps {
		detail := strings.TrimSpace(ts.Detail)
		if detail == "" && ts.FixCommand != "" {
			detail = "run: " + ts.FixCommand
		}
		out = append(out, fmt.Sprintf("%s discovery is %s — %s", ts.Tool, ts.Status, detail))
	}
	return out
}

func setupGapNext(gaps []ToolSetup) []string {
	var out []string
	for _, ts := range gaps {
		if strings.TrimSpace(ts.FixCommand) == "" {
			continue
		}
		out = append(out, fmt.Sprintf("run `%s` — %s is %s", ts.FixCommand, ts.Tool, ts.Status))
	}
	return out
}

func setupGapActions(gaps []ToolSetup) []domain.NextAction {
	var out []domain.NextAction
	for _, ts := range gaps {
		if strings.TrimSpace(ts.FixCommand) == "" {
			continue
		}
		out = append(out, domain.NextAction{
			Command:   ts.FixCommand,
			Reason:    fmt.Sprintf("make %s searchable in this workspace before treating investigate as discovery", ts.Tool),
			BlockedBy: []string{string(ts.Status)},
		})
	}
	return out
}

func annotateHealthWithSetup(health []adapters.HealthReport, setup SetupReport) {
	byTool := map[string]ToolSetup{}
	for _, ts := range setup.Tools {
		byTool[ts.Tool] = ts
	}
	for i := range health {
		ts, ok := byTool[health[i].Tool]
		if !ok {
			continue
		}
		health[i].Index = string(ts.Status)
		health[i].FixCommand = ts.FixCommand
		if ts.Detail != "" && health[i].Detail == "" {
			health[i].Detail = ts.Detail
		} else if ts.Status == SetupNeedsIndex || ts.Status == SetupError || ts.Status == SetupStale {
			if health[i].Detail == "" {
				health[i].Detail = string(ts.Status)
			}
		}
	}
}
