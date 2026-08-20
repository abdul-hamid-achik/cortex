package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
)

func setupByTool(rep SetupReport) map[string]ToolSetup {
	out := map[string]ToolSetup{}
	for _, ts := range rep.Tools {
		out[ts.Tool] = ts
	}
	return out
}

func TestSetupTimeoutIsErrorNotNeedsIndex(t *testing.T) {
	ws := testRepo(t)
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		byOp: map[string]adapters.Result{
			"status": {
				Status:  adapters.StatusPartial,
				Summary: "vecgrep status timed out after 5s — specialist may be slow, not necessarily unindexed",
				Facts:   []adapters.Fact{{Attributes: map[string]string{"index": "timeout"}}},
			},
		}}
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		byOp: map[string]adapters.Result{
			"status": {Status: adapters.StatusAuthoritative},
		}}
	k := newTestKernel(t, ws, cm, vg)
	rep := k.Setup(context.Background())
	ts := setupByTool(rep)["vecgrep"]
	if ts.Status != SetupError {
		t.Fatalf("timeout should be SetupError, got %+v", ts)
	}
	if ts.FixCommand != "" {
		t.Errorf("timeout must not recommend index, fix=%q", ts.FixCommand)
	}
}

func TestSetupUsesStatusProbeNotSearch(t *testing.T) {
	ws := testRepo(t)
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		byOp: map[string]adapters.Result{
			"status": {Status: adapters.StatusAuthoritative, Summary: "ready"},
			"find":   {Status: adapters.StatusError, Summary: "should not run"},
		}}
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		byOp: map[string]adapters.Result{
			"status": {Status: adapters.StatusUnavailable, Summary: "collection not found: chunks",
				Facts: []adapters.Fact{{Kind: "tool_unavailable", Attributes: map[string]string{
					"fix": "vecgrep reset --force && vecgrep index",
				}}}},
			"search": {Status: adapters.StatusError, Summary: "should not run"},
		}}
	k := newTestKernel(t, ws, cm, vg)
	rep := k.Setup(context.Background())
	byTool := setupByTool(rep)
	if byTool["codemap"].Status != SetupReady {
		t.Fatalf("codemap = %+v", byTool["codemap"])
	}
	if byTool["vecgrep"].Status != SetupNeedsIndex {
		t.Fatalf("vecgrep = %+v", byTool["vecgrep"])
	}
	if byTool["vecgrep"].FixCommand != "vecgrep reset --force && vecgrep index" {
		t.Errorf("fix = %q", byTool["vecgrep"].FixCommand)
	}
	for _, req := range vg.requests() {
		if req.Operation == "search" {
			t.Fatalf("setup must not call vecgrep search, got %+v", req)
		}
	}
	for _, req := range cm.requests() {
		if req.Operation == "find" {
			t.Fatalf("setup must not call codemap find, got %+v", req)
		}
	}
}

func TestSetupReportsReadyAndNeedsIndex(t *testing.T) {
	ws := testRepo(t)
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusUnavailable, Summary: "no index in this workspace"}}
	k := newTestKernel(t, ws, cm, vg)

	rep := k.Setup(context.Background())
	if !rep.IsRepo {
		t.Error("testRepo should be a git repo")
	}
	byTool := setupByTool(rep)
	if ts := byTool["codemap"]; ts.Status != SetupReady || !ts.Installed {
		t.Errorf("codemap = %+v, want ready+installed", ts)
	}
	vgts := byTool["vecgrep"]
	if vgts.Status != SetupNeedsIndex || !vgts.Installed {
		t.Errorf("vecgrep = %+v, want needs_index+installed", vgts)
	}
	if vgts.FixCommand == "" {
		t.Error("a needs_index tool should carry a fix command")
	}
}

func TestSetupReportsMissingTools(t *testing.T) {
	k := newTestKernel(t, testRepo(t)) // no codemap/vecgrep registered
	rep := k.Setup(context.Background())
	if len(rep.Tools) == 0 {
		t.Fatal("setup should report a readiness entry per discovery tool")
	}
	for _, ts := range rep.Tools {
		if ts.Status != SetupMissing || ts.Installed {
			t.Errorf("%s = %+v, want missing+not-installed", ts.Tool, ts)
		}
	}
}

func TestSetupDetectsProjectConfig(t *testing.T) {
	ws := testRepo(t)
	if rep := newTestKernel(t, ws).Setup(context.Background()); rep.HasConfig || rep.VerifierCount != 0 {
		t.Errorf("no cortex.yaml should mean HasConfig=false, got %+v", rep)
	}

	cfg := "verifiers:\n  unit:\n    argv: [\"go\", \"test\"]\n    kind: unit_test\n    surface: code\n    timeout: 1m\n"
	if err := os.WriteFile(filepath.Join(ws, "cortex.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := newTestKernel(t, ws).Setup(context.Background())
	if !rep.HasConfig || rep.VerifierCount != 1 {
		t.Errorf("HasConfig=%v VerifierCount=%d, want true/1", rep.HasConfig, rep.VerifierCount)
	}
}

func TestSetupProbeErrorIsReported(t *testing.T) {
	ws := testRepo(t)
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusError, Summary: "codemap crashed"}}
	k := newTestKernel(t, ws, cm)
	rep := k.Setup(context.Background())
	if ts := setupByTool(rep)["codemap"]; ts.Status != SetupNeedsIndex {
		// A non-authoritative probe from an installed tool maps to needs_index
		// (the actionable state); the detail carries the specific reason.
		t.Errorf("codemap = %+v, want needs_index with detail", ts)
	}
}

func TestStatusFullAnnotatesIndexReadiness(t *testing.T) {
	ws := testRepo(t)
	cm := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusUnavailable, Summary: "no index in this workspace"}}
	k := newTestKernel(t, ws, cm, vg)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "fix refund"})
	if err != nil || !started.OK {
		t.Fatalf("start: %v %+v", err, started)
	}
	rep, err := k.Status(context.Background(), started.TaskID, "full")
	if err != nil || !rep.OK {
		t.Fatalf("status: %v %+v", err, rep)
	}
	byTool := map[string]adapters.HealthReport{}
	for _, h := range rep.ToolHealth {
		byTool[h.Tool] = h
	}
	vgHealth := byTool["vecgrep"]
	if !vgHealth.Available {
		t.Fatalf("vecgrep binary is present, available=%v", vgHealth.Available)
	}
	if vgHealth.Index != string(SetupNeedsIndex) {
		t.Errorf("vecgrep index = %q, want needs_index", vgHealth.Index)
	}
	if vgHealth.FixCommand == "" {
		t.Error("vecgrep fixCommand should name vecgrep init/index")
	}
	joined := strings.Join(rep.Warnings, " ")
	if !strings.Contains(joined, "vecgrep") || !strings.Contains(joined, "needs_index") {
		t.Errorf("status warnings should name the index gap, got %v", rep.Warnings)
	}
	foundFix := false
	for _, a := range rep.Actions {
		if strings.Contains(a.Command, "vecgrep") {
			foundFix = true
		}
	}
	if !foundFix {
		t.Errorf("status should offer a vecgrep index action, got %+v", rep.Actions)
	}
}
