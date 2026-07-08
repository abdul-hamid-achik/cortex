// Package config resolves Cortex's paths and runtime policy. Case files live
// repository-local under <workspace>/.agent/cases for active work (SPEC §8.1);
// global state (registry defaults) lives under $CORTEX_HOME or ~/.cortex.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// AgentDir is the repository-local state directory name.
const AgentDir = ".agent"

// Config holds resolved runtime policy for a kernel instance.
type Config struct {
	// Workspace is the absolute path to the repository/working directory.
	Workspace string
	// CasesDir is where case files are written (<workspace>/.agent/cases).
	CasesDir string
	// Budget bounds tool use per workflow (SPEC §7.3).
	Budget domain.Budget
	// RedactLiterals are extra exact strings to always mask (e.g. known secret
	// names surfaced by tvault). Never populate this with secret values.
	RedactLiterals []string
	// sources records which config files were applied (increasing precedence).
	sources []string
}

// For resolves configuration for a given workspace directory: built-in defaults
// layered with any cortex.yaml files and CORTEX_* env overrides (SPEC §27). A
// blank workspace falls back to the current working directory.
func For(workspace string) Config {
	ws := ExpandPath(workspace)
	if ws == "" {
		if wd, err := os.Getwd(); err == nil {
			ws = wd
		} else {
			ws = "."
		}
	}
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}
	cfg := Config{
		Workspace: ws,
		CasesDir:  filepath.Join(ws, AgentDir, "cases"),
		Budget:    domain.DefaultBudget(),
	}
	load(&cfg)
	return cfg
}

// Home returns the global Cortex state directory ($CORTEX_HOME or ~/.cortex).
func Home() string {
	if h := os.Getenv("CORTEX_HOME"); h != "" {
		return ExpandPath(h)
	}
	if hd, err := os.UserHomeDir(); err == nil {
		return filepath.Join(hd, ".cortex")
	}
	return filepath.Join(".", ".cortex")
}

// ExpandPath expands a leading ~ (only "~" itself or "~/…", so a real file
// named "~foo" is left alone). It deliberately does NOT run os.ExpandEnv: the
// shell already expands env vars in CLI args and env values, and applying it to
// a real filesystem path would corrupt a legitimate path containing a '$'.
func ExpandPath(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		if hd, err := os.UserHomeDir(); err == nil {
			return hd
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if hd, err := os.UserHomeDir(); err == nil {
			return filepath.Join(hd, p[2:])
		}
	}
	return p
}
