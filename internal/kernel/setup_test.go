package kernel

import (
	"context"
	"os"
	"path/filepath"
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
