package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points Cortex's global dirs at a throwaway temp base so tests never
// read or write the real ~/.config, ~/.local/state, or ~/.cortex. Returns the base.
func isolate(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("CORTEX_HOME", base)
	return base
}

func TestForDefaults(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	cfg := For(dir)
	if cfg.Budget.MaxParallelCalls != 3 || cfg.Budget.MaxInvestigationRounds != 3 {
		t.Errorf("expected default budget, got %+v", cfg.Budget)
	}
	// A fresh workspace (no repo-local .cortex) defaults to the central XDG tree.
	want := filepath.Join(SessionsRoot(), Slug(dir))
	if cfg.CasesDir != want {
		t.Errorf("cases dir = %s, want %s", cfg.CasesDir, want)
	}
	if len(cfg.Sources()) != 0 {
		t.Errorf("expected no config sources, got %v", cfg.Sources())
	}
}

func TestForHonorsRepoLocalCases(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	// A pre-existing repo-local store keeps being used — upgrading to the central
	// default must not strand active work.
	local := filepath.Join(dir, ".cortex", "cases")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg := For(dir); cfg.CasesDir != local {
		t.Errorf("repo-local .cortex/cases should win, got %s want %s", cfg.CasesDir, local)
	}
}

func TestForLoadsFile(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	yaml := `budget:
  max_investigation_rounds: 7
  max_parallel_calls: 5
redact_literals:
  - MY_SECRET_NAME
  - ANOTHER_ONE
cases_dir: custom/cases
`
	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := For(dir)
	if cfg.Budget.MaxInvestigationRounds != 7 {
		t.Errorf("file should override investigation rounds, got %d", cfg.Budget.MaxInvestigationRounds)
	}
	if cfg.Budget.MaxParallelCalls != 5 {
		t.Errorf("file should override parallel calls, got %d", cfg.Budget.MaxParallelCalls)
	}
	// Unspecified fields keep their defaults.
	if cfg.Budget.MaxEvidenceItemsReturned != 12 {
		t.Errorf("unspecified field should keep default, got %d", cfg.Budget.MaxEvidenceItemsReturned)
	}
	if len(cfg.RedactLiterals) != 2 {
		t.Errorf("expected 2 redact literals, got %v", cfg.RedactLiterals)
	}
	if cfg.CasesDir != filepath.Join(dir, "custom", "cases") {
		t.Errorf("relative cases_dir should resolve against workspace, got %s", cfg.CasesDir)
	}
	if len(cfg.Sources()) != 1 {
		t.Errorf("expected 1 applied source, got %v", cfg.Sources())
	}
}

func TestEnvOverridesFile(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte("budget:\n  max_parallel_calls: 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORTEX_MAX_PARALLEL_CALLS", "9")
	cfg := For(dir)
	if cfg.Budget.MaxParallelCalls != 9 {
		t.Errorf("env should win over file, got %d", cfg.Budget.MaxParallelCalls)
	}
}

func TestEnvOverridesAutoRetries(t *testing.T) {
	// Regression: CORTEX_MAX_AUTO_RETRIES was parsed by the file layer but had no
	// env override, so it silently ignored the variable.
	isolate(t)
	dir := t.TempDir()
	t.Setenv("CORTEX_MAX_AUTO_RETRIES", "0")
	cfg := For(dir)
	if cfg.Budget.MaxAutoRetriesPerTool != 0 {
		t.Errorf("env should set auto-retries to 0, got %d", cfg.Budget.MaxAutoRetriesPerTool)
	}
}

func TestCasesDirOutsideWorkspace(t *testing.T) {
	// Absolute / home-relative cases_dir keeps the workspace free of Cortex state.
	isolate(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "off-repo-cases")
	t.Setenv("CORTEX_CASES_DIR", outside)
	cfg := For(dir)
	if cfg.CasesDir != outside {
		t.Errorf("absolute CORTEX_CASES_DIR should win, got %s want %s", cfg.CasesDir, outside)
	}
}

func TestDefaultCasesDir(t *testing.T) {
	base := isolate(t)
	ws := t.TempDir()
	// No repo-local store: central XDG location under the (isolated) base.
	want := filepath.Join(base, "sessions", Slug(ws))
	if got := DefaultCasesDir(ws); got != want {
		t.Errorf("DefaultCasesDir = %s, want %s", got, want)
	}
	// Repo-local store present: it wins.
	local := filepath.Join(ws, ".cortex", "cases")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefaultCasesDir(ws); got != local {
		t.Errorf("DefaultCasesDir with repo-local = %s, want %s", got, local)
	}
}

func TestEnsureStateIgnored(t *testing.T) {
	ws := t.TempDir()
	// A repo-local cases dir gets a .gitignore ("*") at its parent (.cortex/).
	EnsureStateIgnored(ws, filepath.Join(ws, ".cortex", "cases"))
	gi := filepath.Join(ws, ".cortex", ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		t.Fatalf("expected workspace-local .cortex/.gitignore, got %v", err)
	}
	body, _ := os.ReadFile(gi)
	if !strings.Contains(string(body), "*") {
		t.Errorf("ignore file should contain the catch-all glob, got %q", body)
	}
	// A cases dir outside the workspace must NOT get a stray ignore file.
	outside := filepath.Join(t.TempDir(), "off-repo", "cases")
	EnsureStateIgnored(ws, outside)
	if _, err := os.Stat(filepath.Join(filepath.Dir(outside), ".gitignore")); err == nil {
		t.Error("must not write .gitignore outside the workspace for an external cases_dir")
	}
	// cases_dir == workspace (e.g. `cases_dir: .`) must NOT write "*" into the
	// workspace's PARENT directory — a data-safety regression guard.
	ws2 := t.TempDir()
	EnsureStateIgnored(ws2, ws2)
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws2), ".gitignore")); err == nil {
		t.Error("must not write .gitignore in the workspace's parent when cases_dir == workspace")
	}
	// Empty cases dir is a no-op.
	EnsureStateIgnored(ws, "")
}

func TestExpandPathSafe(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := map[string]string{
		"":                "",
		"~":               home,
		"~/x":             filepath.Join(home, "x"),
		"~foo":            "~foo",            // a real file named ~foo is left alone
		"/etc/passwd":     "/etc/passwd",     // absolute path untouched
		"/tmp/$NOT_A_VAR": "/tmp/$NOT_A_VAR", // no env expansion — a literal $ survives
		"rel/path":        "rel/path",        // relative path untouched
	}
	for in, want := range cases {
		if got := ExpandPath(in); got != want {
			t.Errorf("ExpandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMalformedFileIgnored(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte("budget: [this is not valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := For(dir) // must not panic; falls back to defaults
	if cfg.Budget.MaxParallelCalls != 3 {
		t.Errorf("malformed config should fall back to defaults, got %d", cfg.Budget.MaxParallelCalls)
	}
}
