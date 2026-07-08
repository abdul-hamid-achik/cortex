package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := For(dir)
	if cfg.Budget.MaxParallelCalls != 3 || cfg.Budget.MaxInvestigationRounds != 3 {
		t.Errorf("expected default budget, got %+v", cfg.Budget)
	}
	if cfg.CasesDir != filepath.Join(dir, ".agent", "cases") {
		t.Errorf("unexpected cases dir: %s", cfg.CasesDir)
	}
	if len(cfg.Sources()) != 0 {
		t.Errorf("expected no config sources, got %v", cfg.Sources())
	}
}

func TestForLoadsFile(t *testing.T) {
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
	dir := t.TempDir()
	t.Setenv("CORTEX_MAX_AUTO_RETRIES", "0")
	cfg := For(dir)
	if cfg.Budget.MaxAutoRetriesPerTool != 0 {
		t.Errorf("env should set auto-retries to 0, got %d", cfg.Budget.MaxAutoRetriesPerTool)
	}
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte("budget: [this is not valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := For(dir) // must not panic; falls back to defaults
	if cfg.Budget.MaxParallelCalls != 3 {
		t.Errorf("malformed config should fall back to defaults, got %d", cfg.Budget.MaxParallelCalls)
	}
}
