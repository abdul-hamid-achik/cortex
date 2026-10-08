package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func verifierSummary(vs []VerifierSuggestion) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name + "/" + v.Kind + "=" + strings.Join(v.Argv, " ")
	}
	return out
}

func TestDetectVerifiersTaskRunners(t *testing.T) {
	const taskfileAll = "version: '3'\ntasks:\n  test:\n    cmds: [go test ./...]\n  build:\n    cmds: [go build ./...]\n  lint:\n    cmds: [golangci-lint run]\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "taskfile test build lint",
			files: map[string]string{"Taskfile.yml": taskfileAll},
			want:  []string{"unit/unit_test=task test", "build/build=task build", "lint/lint=task lint"},
		},
		{
			name:  "taskfile yaml extension",
			files: map[string]string{"Taskfile.yaml": taskfileAll},
			want:  []string{"unit/unit_test=task test", "build/build=task build", "lint/lint=task lint"},
		},
		{
			name: "taskfile wins over ecosystem marker",
			files: map[string]string{
				"go.mod":       "module x\n",
				"Taskfile.yml": "version: '3'\ntasks:\n  test: {cmds: [echo]}\n",
			},
			want: []string{"unit/unit_test=task test"},
		},
		{
			name: "taskfile without test falls back to go.mod and keeps build",
			files: map[string]string{
				"go.mod":       "module x\n",
				"Taskfile.yml": "version: '3'\ntasks:\n  build: {cmds: [echo]}\n  check: {cmds: [echo]}\n",
			},
			want: []string{"unit/unit_test=go test ./...", "build/build=task build"},
		},
		{
			name: "taskfile with only check is not guessed",
			files: map[string]string{
				"go.mod":       "module x\n",
				"Taskfile.yml": "version: '3'\ntasks:\n  check: {cmds: [echo]}\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name: "taskfile test key outside tasks is ignored",
			files: map[string]string{
				"go.mod":       "module x\n",
				"Taskfile.yml": "version: '3'\nvars:\n  test: x\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name:  "makefile test target",
			files: map[string]string{"Makefile": "build:\n\tgo build\n\ntest:\n\tgo test ./...\n\nlint: build\n\tvet\n"},
			want:  []string{"unit/unit_test=make test", "build/build=make build", "lint/lint=make lint"},
		},
		{
			name:  "makefile multi target rule",
			files: map[string]string{"Makefile": "test lint:\n\techo\n"},
			want:  []string{"unit/unit_test=make test", "lint/lint=make lint"},
		},
		{
			name: "makefile phony only is not a target",
			files: map[string]string{
				"go.mod":   "module x\n",
				"Makefile": ".PHONY: test build\nall:\n\techo\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name: "makefile ignores recipe lines comments and assignments",
			files: map[string]string{
				"go.mod":   "module x\n",
				"Makefile": "# test:\nall:\n\ttest: echo\n  test:\ntest := 1\nTEST_FLAGS = a:b\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name:  "justfile test recipe",
			files: map[string]string{"justfile": "set shell := [\"bash\", \"-c\"]\n\ntest:\n    cargo test\n\nbuild target=\"debug\":\n    cargo build\n"},
			want:  []string{"unit/unit_test=just test", "build/build=just build"},
		},
		{
			name:  "capitalized Justfile with quiet recipe and dependencies",
			files: map[string]string{"Justfile": "@test: build\n    echo\nbuild:\n    echo\n"},
			want:  []string{"unit/unit_test=just test", "build/build=just build"},
		},
		{
			name: "justfile assignment and similar names are not recipes",
			files: map[string]string{
				"go.mod":   "module x\n",
				"justfile": "test := \"x\"\ntest-all:\n    echo\ntesting:\n    echo\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name: "taskfile wins over makefile and justfile",
			files: map[string]string{
				"Taskfile.yml": "version: '3'\ntasks:\n  test: {cmds: [echo]}\n",
				"Makefile":     "test:\n\techo\nlint:\n\techo\n",
				"justfile":     "test:\n    echo\n",
			},
			want: []string{"unit/unit_test=task test"},
		},
		{
			name: "makefile wins over justfile",
			files: map[string]string{
				"Makefile": "test:\n\techo\n",
				"justfile": "test:\n    echo\n",
			},
			want: []string{"unit/unit_test=make test"},
		},
		{
			name: "runner test replaces multiple ecosystems",
			files: map[string]string{
				"go.mod":       "module x\n",
				"package.json": "{}",
				"Makefile":     "test:\n\techo\n",
			},
			want: []string{"unit/unit_test=make test"},
		},
		{
			name: "malformed taskfile falls back gracefully",
			files: map[string]string{
				"go.mod":       "module x\n",
				"Taskfile.yml": "tasks: [unterminated\n  test: {{{\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name: "taskfile tasks of wrong shape falls back gracefully",
			files: map[string]string{
				"go.mod":       "module x\n",
				"Taskfile.yml": "tasks:\n  - test\n",
			},
			want: []string{"unit/unit_test=go test ./..."},
		},
		{
			name: "malformed taskfile still lets makefile win",
			files: map[string]string{
				"Taskfile.yml": ":\n\t- [",
				"Makefile":     "test:\n\techo\n",
			},
			want: []string{"unit/unit_test=make test"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			for name, content := range tc.files {
				writeMarker(t, ws, name, content)
			}
			got := verifierSummary(DetectVerifiers(ws))
			if strings.Join(got, "; ") != strings.Join(tc.want, "; ") {
				t.Fatalf("verifiers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectVerifiersRunnerFileIsBounded(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "go.mod", "module x\n")
	// The test target sits past the read cap, so it must not be seen.
	big := strings.Repeat("# filler line to push the target past the cap\n", (maxRunnerFileBytes/45)+10)
	writeMarker(t, ws, "Makefile", big+"test:\n\techo\n")

	got := verifierSummary(DetectVerifiers(ws))
	want := []string{"unit/unit_test=go test ./..."}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("verifiers = %v, want %v", got, want)
	}

	// A target inside the cap of an equally large file is still found.
	writeMarker(t, ws, "Makefile", "test:\n\techo\n"+big)
	got = verifierSummary(DetectVerifiers(ws))
	if len(got) != 1 || got[0] != "unit/unit_test=make test" {
		t.Fatalf("verifiers = %v, want make test", got)
	}
}

func TestDetectVerifiersRunnerDirectoryIgnored(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "go.mod", "module x\n")
	if err := os.Mkdir(filepath.Join(ws, "Makefile"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := verifierSummary(DetectVerifiers(ws))
	if len(got) != 1 || got[0] != "unit/unit_test=go test ./..." {
		t.Fatalf("verifiers = %v", got)
	}
}

func TestRenderInitYAMLTaskRunnerRoundTripsAndWarns(t *testing.T) {
	ws := t.TempDir()
	writeMarker(t, ws, "Taskfile.yml", "version: '3'\ntasks:\n  test: {cmds: [echo]}\n  build: {cmds: [echo]}\n  lint: {cmds: [echo]}\n")

	content := RenderInitYAML(DetectVerifiers(ws))
	if !strings.Contains(content, "CORTEX_APPROVE_COMMANDS=1") || !strings.Contains(content, "recipe code written in this repository") {
		t.Fatalf("header must keep the approval gate and warn about recipe code:\n%s", content)
	}
	writeMarker(t, ws, "cortex.yaml", content)

	cfg := For(ws)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("generated config is invalid against the real loader: %v\ncontent:\n%s", err, content)
	}
	for name, kind := range map[string]domain.EvidenceKind{"unit": domain.KindUnitTest, "build": domain.KindBuild, "lint": domain.KindLint} {
		v, ok := cfg.Verifiers[name]
		if !ok || v.Kind != kind {
			t.Fatalf("verifier %q = %+v (ok=%v), want kind %s", name, v, ok, kind)
		}
	}
}

func TestRenderInitYAMLEcosystemOnlyOmitsRecipeWarning(t *testing.T) {
	content := RenderInitYAML([]VerifierSuggestion{{Name: "unit", Argv: []string{"go", "test", "./..."}, Kind: "unit_test", Surface: "code", Timeout: "5m"}})
	if strings.Contains(content, "recipe code") {
		t.Fatalf("ecosystem verifiers should not carry the recipe warning:\n%s", content)
	}
}
