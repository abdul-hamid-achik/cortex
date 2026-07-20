package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VerifierSuggestion is one command verifier that `cortex init` detected from
// the workspace's project markers. Argv is built from a fixed, known-safe
// template per ecosystem — never from user-supplied text — so init can show it
// for review. This is the deliberate exception to `cortex config` hiding argv:
// config masks user-written commands that may carry sensitive local paths,
// while init only ever emits templates it generated itself.
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
// and returns a command verifier per detected test runner. A single detected
// ecosystem is named "unit" (the ergonomic default); several are named by
// ecosystem so their verifier names stay distinct. Detection is deliberately
// shallow — marker files only — so it never reads file contents or runs
// anything.
func DetectVerifiers(workspace string) []VerifierSuggestion {
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

	out := make([]VerifierSuggestion, 0, len(found))
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
