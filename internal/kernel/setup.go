package kernel

import (
	"context"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
)

// SetupStatus is the readiness state of one discovery/structure dependency for
// a workspace.
type SetupStatus string

const (
	// SetupReady means the tool is installed and has a usable index.
	SetupReady SetupStatus = "ready"
	// SetupNeedsIndex means the tool is installed but has no usable index (absent
	// or broken, e.g. vecgrep's "embedding profile is missing").
	SetupNeedsIndex SetupStatus = "needs_index"
	// SetupMissing means the binary is not on PATH.
	SetupMissing SetupStatus = "missing"
	// SetupError means the tool is installed but the readiness probe failed
	// unexpectedly.
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
	{tool: "codemap", op: "find", fix: "codemap index"},
	{tool: "vecgrep", op: "search", fix: "vecgrep init && vecgrep index"},
}

// setupProbeQuery is a neutral, non-empty token used only to elicit each tool's
// index-status signal; the query content is irrelevant to readiness.
const setupProbeQuery = "setup"

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

// probeSetupTool checks one tool's install + index readiness. A search probe's
// authoritative result means a usable index; any non-authoritative result
// (unavailable/partial/error) from an installed tool means the index is absent
// or broken — the same signal investigate's discovery surfaces mid-task.
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
		"dir": k.cfg.Workspace, "query": setupProbeQuery, "top": 1, "limit": 1,
	}})
	if err != nil {
		ts.Status = SetupError
		ts.Detail = err.Error()
		return ts
	}
	if res.Status == adapters.StatusAuthoritative {
		ts.Status = SetupReady
		return ts
	}
	ts.Status = SetupNeedsIndex
	ts.Detail = res.Summary
	return ts
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
