package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxRunnerFileBytes caps how much of a Taskfile, Makefile, or justfile init
// reads when scanning for task names. Larger files are scanned only up to the
// cap, so a hostile or generated file cannot make init read unbounded input.
const maxRunnerFileBytes = 256 << 10

// VerifierSuggestion is one command verifier that `cortex init` detected from
// the workspace's project markers. Argv is built from a fixed template per
// ecosystem or task runner — only the runner name and a recognized target name
// (test, build, lint) are ever emitted, never text copied from the repository —
// so init can show it for review. This is the deliberate exception to
// `cortex config` hiding argv: config masks user-written commands that may
// carry sensitive local paths, while init only ever emits templates it
// generated itself.
//
// A fixed template is not a safety guarantee. `task`, `make`, and `just`
// execute repository-authored recipe code, so those suggestions are not
// known-safe; the boundary is the trusted launcher's CORTEX_APPROVE_COMMANDS=1
// (or the out-of-repo digest grant from `cortex setup --trust-commands`), which
// repository configuration can never provide for itself.
type VerifierSuggestion struct {
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Kind    string   `json:"kind"`
	Surface string   `json:"surface"`
	Timeout string   `json:"timeout"`
	Reason  string   `json:"reason"`
}

// InitResult reports what `cortex init` detected and whether it wrote a config.
// When a project config already exists and force is false, Created is false and
// Existed is true; nothing is written.
type InitResult struct {
	Workspace  string               `json:"workspace"`
	ConfigPath string               `json:"configPath"`
	Created    bool                 `json:"created"`
	Existed    bool                 `json:"existed"`
	Existing   []string             `json:"existingConfigs"`
	Detected   []VerifierSuggestion `json:"verifiers"`
	Content    string               `json:"content"`
}

// InitConfigPath is the canonical project config location `cortex init` writes
// to: cortex.yaml at the workspace root (the highest-precedence project path in
// searchPaths).
func InitConfigPath(workspace string) string {
	return filepath.Join(workspace, "cortex.yaml")
}

// projectConfigPaths are the project-scoped config files init refuses to
// clobber. The global Home()/config.yaml is intentionally excluded — it is not
// project-specific and init only manages project configuration.
func projectConfigPaths(workspace string) []string {
	return []string{
		filepath.Join(workspace, ".config", "cortex.yaml"),
		filepath.Join(workspace, "cortex.yml"),
		filepath.Join(workspace, "cortex.yaml"),
	}
}

// Init generates a starter cortex.yaml for the workspace. It detects the
// project's test runner, renders a command verifier for it, and writes
// cortex.yaml — unless a project config already exists and force is false. A
// blank workspace falls back to the current working directory, mirroring For.
func Init(workspace string, force bool) (InitResult, error) {
	ws := ExpandPath(workspace)
	if ws == "" {
		if wd, err := os.Getwd(); err == nil {
			ws = wd
		}
	}
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}

	res := InitResult{
		Workspace:  ws,
		ConfigPath: InitConfigPath(ws),
		Existing:   []string{},
	}
	for _, p := range projectConfigPaths(ws) {
		if isFile(p) {
			res.Existing = append(res.Existing, p)
		}
	}
	res.Detected = DetectVerifiers(ws)
	res.Content = RenderInitYAML(res.Detected)

	if len(res.Existing) > 0 {
		res.Existed = true
		if !force {
			return res, nil
		}
	}
	// #nosec G306 -- cortex.yaml is a non-secret project config file, world-readable by design.
	if err := os.WriteFile(res.ConfigPath, []byte(res.Content), 0o644); err != nil {
		return res, fmt.Errorf("write %s: %w", res.ConfigPath, err)
	}
	res.Created = true
	return res, nil
}

// DetectVerifiers inspects the workspace root for well-known project markers
// and returns command verifiers for the detected checks. A task runner file
// (Taskfile.yml/yaml, Makefile, justfile) that defines a `test` task takes
// precedence over ecosystem markers, and `build`/`lint` tasks of the same
// runner yield `build`/`lint` verifiers. Without a runner test task, a single
// detected ecosystem is named "unit" (the ergonomic default); several are named
// by ecosystem so their verifier names stay distinct. Detection is read-only:
// marker files are checked, and runner files get a bounded text scan for task
// names. Nothing is ever executed.
func DetectVerifiers(workspace string) []VerifierSuggestion {
	runner := detectRunner(workspace)
	if runner.targets["test"] {
		out := []VerifierSuggestion{runner.suggestion("unit", "unit_test", "test")}
		return append(out, runner.extras()...)
	}

	type candidate struct {
		eco    string
		argv   []string
		reason string
	}
	var found []candidate
	if isFile(filepath.Join(workspace, "go.mod")) {
		found = append(found, candidate{"go", []string{"go", "test", "./..."}, "go.mod found"})
	}
	if isFile(filepath.Join(workspace, "Cargo.toml")) {
		found = append(found, candidate{"rust", []string{"cargo", "test"}, "Cargo.toml found"})
	}
	if isFile(filepath.Join(workspace, "package.json")) {
		argv, reason := nodeTestCommand(workspace)
		found = append(found, candidate{"node", argv, reason})
	}
	if marker := pythonMarker(workspace); marker != "" {
		found = append(found, candidate{"python", []string{"python", "-m", "pytest"}, marker + " found"})
	}
	// Godot projects are detected (project.godot) but no verifier is emitted —
	// Cortex recognizes .gd as code but does not exec the Godot binary.

	out := make([]VerifierSuggestion, 0, len(found)+2)
	for _, c := range found {
		name := c.eco
		if len(found) == 1 {
			name = "unit"
		}
		out = append(out, VerifierSuggestion{
			Name:    name,
			Argv:    c.argv,
			Kind:    "unit_test",
			Surface: "code",
			Timeout: "5m",
			Reason:  c.reason,
		})
	}
	return append(out, runner.extras()...)
}

// runnerInfo is the task runner file init found and the recognized targets
// (test, build, lint) it defines.
type runnerInfo struct {
	binary  string // task | make | just
	file    string // file name the targets were read from
	targets map[string]bool
}

// suggestion builds a verifier that runs `<binary> <target>`.
func (r runnerInfo) suggestion(name, kind, target string) VerifierSuggestion {
	return VerifierSuggestion{
		Name:    name,
		Argv:    []string{r.binary, target},
		Kind:    kind,
		Surface: "code",
		Timeout: "5m",
		Reason:  r.file + " defines " + r.targetNoun() + " " + target,
	}
}

func (r runnerInfo) targetNoun() string {
	switch r.binary {
	case "make":
		return "target"
	case "just":
		return "recipe"
	default:
		return "task"
	}
}

// extras returns the build and lint verifiers the runner defines.
func (r runnerInfo) extras() []VerifierSuggestion {
	var out []VerifierSuggestion
	if r.targets["build"] {
		out = append(out, r.suggestion("build", "build", "build"))
	}
	if r.targets["lint"] {
		out = append(out, r.suggestion("lint", "lint", "lint"))
	}
	return out
}

// runnerTargets are the only target names init looks for. Anything else (for
// example `check`) is deliberately not guessed at.
var runnerTargets = []string{"test", "build", "lint"}

// detectRunner returns the first task runner file, in precedence order
// Taskfile, Makefile, justfile, that defines at least one recognized target.
func detectRunner(workspace string) runnerInfo {
	for _, spec := range []struct {
		binary string
		files  []string
		scan   func([]byte) map[string]bool
	}{
		{"task", []string{"Taskfile.yml", "Taskfile.yaml"}, taskfileTargets},
		{"make", []string{"Makefile", "makefile", "GNUmakefile"}, makefileTargets},
		{"just", []string{"justfile", "Justfile"}, justfileTargets},
	} {
		for _, name := range spec.files {
			data, ok := readBounded(filepath.Join(workspace, name))
			if !ok {
				continue
			}
			if targets := spec.scan(data); len(targets) > 0 {
				return runnerInfo{binary: spec.binary, file: name, targets: targets}
			}
		}
	}
	return runnerInfo{}
}

// readBounded reads at most maxRunnerFileBytes of a regular file.
func readBounded(path string) ([]byte, bool) {
	if !isFile(path) {
		return nil, false
	}
	f, err := os.Open(path) // #nosec G304 -- fixed marker names under the workspace root.
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxRunnerFileBytes))
	if err != nil {
		return nil, false
	}
	return data, true
}

// taskfileTargets parses a Taskfile and reports which recognized tasks it
// defines under `tasks:`. Malformed or truncated YAML yields no targets.
func taskfileTargets(data []byte) map[string]bool {
	var doc struct {
		Tasks map[string]yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, name := range runnerTargets {
		if _, ok := doc.Tasks[name]; ok {
			out[name] = true
		}
	}
	return out
}

// makefileTargets scans for rule lines (`test:`, `test lint:`) at the start of
// a line. `.PHONY: test` only lists a prerequisite and does not define a rule,
// and variable assignments (`test := x`) are not rules.
func makefileTargets(data []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		text := string(line)
		if text == "" || text[0] == '\t' || text[0] == ' ' || text[0] == '#' {
			continue
		}
		colon := strings.IndexByte(text, ':')
		if colon < 0 || strings.ContainsAny(text[:colon], "=$") {
			continue
		}
		if strings.HasPrefix(text[colon+1:], "=") {
			continue
		}
		for _, field := range strings.Fields(text[:colon]) {
			for _, name := range runnerTargets {
				if field == name {
					out[name] = true
				}
			}
		}
	}
	return out
}

// justfileTargets scans for recipe lines (`test:`, `test arg:`, `@test:`) at
// the start of a line. Assignments (`test := x`) are not recipes.
func justfileTargets(data []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		text := strings.TrimPrefix(string(line), "@")
		for _, name := range runnerTargets {
			rest, ok := strings.CutPrefix(text, name)
			if !ok || rest == "" || (rest[0] != ':' && rest[0] != ' ' && rest[0] != '\t') {
				continue
			}
			colon := strings.IndexByte(rest, ':')
			if colon < 0 || strings.HasPrefix(rest[colon+1:], "=") {
				continue
			}
			out[name] = true
		}
	}
	return out
}

// nodeTestCommand picks the test runner from the Node lockfile present, falling
// back to npm. The lockfile is the most reliable signal of the package manager
// a project actually uses.
func nodeTestCommand(workspace string) ([]string, string) {
	switch {
	case isFile(filepath.Join(workspace, "bun.lockb")), isFile(filepath.Join(workspace, "bun.lock")):
		return []string{"bun", "test"}, "package.json + bun lockfile"
	case isFile(filepath.Join(workspace, "pnpm-lock.yaml")):
		return []string{"pnpm", "test"}, "package.json + pnpm lockfile"
	case isFile(filepath.Join(workspace, "yarn.lock")):
		return []string{"yarn", "test"}, "package.json + yarn lockfile"
	default:
		return []string{"npm", "test"}, "package.json found"
	}
}

// pythonMarker returns the first Python project marker found, or "" if none.
func pythonMarker(workspace string) string {
	for _, marker := range []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile", "tox.ini"} {
		if isFile(filepath.Join(workspace, marker)) {
			return marker
		}
	}
	return ""
}

// RenderInitYAML renders the cortex.yaml content for the detected verifiers.
// The output is hand-formatted (rather than yaml.Marshal) so it carries guidance
// comments and matches the flow-style argv used throughout the docs. It always
// produces a file that the real loader accepts — with zero verifiers it writes
// only comments plus a commented example.
func RenderInitYAML(verifiers []VerifierSuggestion) string {
	var b strings.Builder
	b.WriteString("# Cortex configuration — generated by `cortex init`.\n")
	b.WriteString("#\n")
	b.WriteString("# Command verifiers stay blocked until the trusted process launching Cortex\n")
	b.WriteString("# sets CORTEX_APPROVE_COMMANDS=1; repository configuration cannot approve\n")
	b.WriteString("# itself. Review the argv below before enabling it.\n")
	if usesTaskRunner(verifiers) {
		b.WriteString("#\n")
		b.WriteString("# task, make, and just execute recipe code written in this repository, so\n")
		b.WriteString("# those verifiers run whatever the recipe says; read it before approving.\n")
	}
	if len(verifiers) == 0 {
		b.WriteString("#\n")
		b.WriteString("# No known test runner was detected. Add a verifier by hand, e.g.:\n")
		b.WriteString("# verifiers:\n")
		b.WriteString("#   unit:\n")
		b.WriteString("#     argv: [\"your-test\", \"command\"]\n")
		b.WriteString("#     kind: unit_test\n")
		b.WriteString("#     surface: code\n")
		b.WriteString("#     timeout: 5m\n")
		return b.String()
	}
	b.WriteString("verifiers:\n")
	for _, v := range verifiers {
		b.WriteString("  " + v.Name + ":\n")
		b.WriteString("    argv: " + renderArgv(v.Argv) + "\n")
		b.WriteString("    kind: " + v.Kind + "\n")
		b.WriteString("    surface: " + v.Surface + "\n")
		b.WriteString("    timeout: " + v.Timeout + "\n")
	}
	return b.String()
}

// usesTaskRunner reports whether any verifier invokes task, make, or just.
func usesTaskRunner(verifiers []VerifierSuggestion) bool {
	for _, v := range verifiers {
		if len(v.Argv) > 0 {
			switch v.Argv[0] {
			case "task", "make", "just":
				return true
			}
		}
	}
	return false
}

// renderArgv renders a flow-style YAML sequence with each element double-quoted.
func renderArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = yamlQuote(arg)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// yamlQuote renders a double-quoted YAML scalar. The templates init generates
// are simple, but quote defensively so a future template containing a special
// character still emits valid YAML.
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// isFile reports whether path exists and is a regular file.
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
