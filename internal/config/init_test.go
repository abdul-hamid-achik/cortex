package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func writeMarker(t *testing.T, workspace, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectVerifiersSingleEcosystemNamesUnit(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "go.mod", "module example.com/x\n")

	got := DetectVerifiers(ws)
	if len(got) != 1 {
		t.Fatalf("expected 1 verifier, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.Name != "unit" || v.Kind != "unit_test" || v.Surface != "code" || v.Timeout != "5m" {
		t.Fatalf("unexpected verifier: %+v", v)
	}
	wantArgv := []string{"go", "test", "./..."}
	if strings.Join(v.Argv, " ") != strings.Join(wantArgv, " ") {
		t.Fatalf("argv = %v, want %v", v.Argv, wantArgv)
	}
}

func TestDetectVerifiersEachEcosystem(t *testing.T) {
	for _, tc := range []struct {
		name     string
		markers  []string
		wantArgv string
	}{
		{name: "go", markers: []string{"go.mod"}, wantArgv: "go test ./..."},
		{name: "rust", markers: []string{"Cargo.toml"}, wantArgv: "cargo test"},
		{name: "node-npm", markers: []string{"package.json"}, wantArgv: "npm test"},
		{name: "node-pnpm", markers: []string{"package.json", "pnpm-lock.yaml"}, wantArgv: "pnpm test"},
		{name: "node-yarn", markers: []string{"package.json", "yarn.lock"}, wantArgv: "yarn test"},
		{name: "node-bun", markers: []string{"package.json", "bun.lockb"}, wantArgv: "bun test"},
		{name: "python-pyproject", markers: []string{"pyproject.toml"}, wantArgv: "python -m pytest"},
		{name: "python-requirements", markers: []string{"requirements.txt"}, wantArgv: "python -m pytest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			for _, m := range tc.markers {
				writeMarker(t, ws, m, "")
			}
			got := DetectVerifiers(ws)
			if len(got) != 1 {
				t.Fatalf("expected 1 verifier, got %d: %+v", len(got), got)
			}
			if strings.Join(got[0].Argv, " ") != tc.wantArgv {
				t.Fatalf("argv = %v, want %q", got[0].Argv, tc.wantArgv)
			}
		})
	}
}

func TestDetectVerifiersMultipleEcosystemsUseDistinctNames(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "go.mod", "module x\n")
	writeMarker(t, ws, "package.json", "{}")

	got := DetectVerifiers(ws)
	if len(got) != 2 {
		t.Fatalf("expected 2 verifiers, got %d: %+v", len(got), got)
	}
	names := map[string]bool{}
	for _, v := range got {
		names[v.Name] = true
	}
	if !names["go"] || !names["node"] {
		t.Fatalf("expected distinct ecosystem names go/node, got %+v", got)
	}
}

func TestDetectVerifiersNone(t *testing.T) {
	if got := DetectVerifiers(t.TempDir()); len(got) != 0 {
		t.Fatalf("expected no verifiers in an empty dir, got %+v", got)
	}
}

func TestRenderInitYAMLRoundTripsThroughLoader(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "go.mod", "module x\n")

	content := RenderInitYAML(DetectVerifiers(ws))
	writeMarker(t, ws, "cortex.yaml", content)

	cfg := For(ws)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("generated config is invalid against the real loader: %v\ncontent:\n%s", err, content)
	}
	v, ok := cfg.Verifiers["unit"]
	if !ok {
		t.Fatalf("unit verifier not loaded; verifiers=%+v\ncontent:\n%s", cfg.Verifiers, content)
	}
	if v.Kind != domain.KindUnitTest || v.Surface != domain.SurfaceCode {
		t.Fatalf("kind/surface = %s/%s, want unit_test/code", v.Kind, v.Surface)
	}
	if strings.Join(v.Argv, " ") != "go test ./..." {
		t.Fatalf("argv = %v", v.Argv)
	}
}

func TestRenderInitYAMLQuotesSpecialCharacters(t *testing.T) {
	content := RenderInitYAML([]VerifierSuggestion{{
		Name: "unit", Argv: []string{"echo", `a"b`, `c\d`},
		Kind: "unit_test", Surface: "code", Timeout: "5m",
	}})
	ws := t.TempDir()
	writeMarker(t, ws, "cortex.yaml", content)

	cfg := For(ws)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("quoted config invalid: %v\ncontent:\n%s", err, content)
	}
	got := cfg.Verifiers["unit"].Argv
	if strings.Join(got, "|") != `echo|a"b|c\d` {
		t.Fatalf("argv did not survive quoting: %v\ncontent:\n%s", got, content)
	}
}

func TestRenderInitYAMLNoVerifiersStillValid(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "cortex.yaml", RenderInitYAML(nil))

	cfg := For(ws)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("empty template should be valid (comments only): %v", err)
	}
	if len(cfg.Verifiers) != 0 {
		t.Fatalf("expected no verifiers, got %+v", cfg.Verifiers)
	}
}

func TestInitWritesAndRefusesOverwrite(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "go.mod", "module x\n")

	res, err := Init(ws, false)
	if err != nil || !res.Created {
		t.Fatalf("first init: created=%v err=%v", res.Created, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "cortex.yaml")); err != nil {
		t.Fatalf("cortex.yaml not written: %v", err)
	}

	// A second init without --force refuses and preserves the file.
	res2, err := Init(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created || !res2.Existed {
		t.Fatalf("second init should refuse: created=%v existed=%v", res2.Created, res2.Existed)
	}

	// --force overwrites.
	res3, err := Init(ws, true)
	if err != nil || !res3.Created {
		t.Fatalf("force init: created=%v err=%v", res3.Created, err)
	}
}

func TestInitPreservesExistingConfigWithoutForce(t *testing.T) {
	ws := t.TempDir()
	original := "budget:\n  max_investigation_rounds: 4\n"
	writeMarker(t, ws, "cortex.yaml", original)

	res, err := Init(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created {
		t.Fatal("init must not overwrite an existing config without --force")
	}
	data, err := os.ReadFile(filepath.Join(ws, "cortex.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("existing config was modified:\n%s", data)
	}
	if len(res.Existing) != 1 {
		t.Fatalf("expected the existing config to be reported, got %+v", res.Existing)
	}
}

func TestInitNoRunnerWritesValidTemplate(t *testing.T) {
	ws := t.TempDir()
	res, err := Init(ws, false)
	if err != nil || !res.Created {
		t.Fatalf("init: created=%v err=%v", res.Created, err)
	}
	if len(res.Detected) != 0 {
		t.Fatalf("expected no detection, got %+v", res.Detected)
	}
	if err := For(ws).Validate(); err != nil {
		t.Fatalf("generated template is invalid: %v", err)
	}
}
