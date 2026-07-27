// Package config resolves Cortex's paths and runtime policy. Case files default
// to a central, XDG-organized location — $XDG_STATE_HOME/cortex/sessions/<repo>/
// — so every session across every repo is visible and auditable in one place
// A pre-existing repo-local .cortex/cases is honored (so in-flight
// work isn't orphaned by the move), and both are fully overridable via
// cases_dir / CORTEX_CASES_DIR. Config/state/cache dirs follow the XDG Base
// Directory spec (paths.go); $CORTEX_HOME or a legacy ~/.cortex collapses them
// into one directory for single-dir installs.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// StateDir is the repository-local Cortex state directory name (default cases
// root is StateDir/cases). Not ".agent" — that name is shared by many tools and
// pollutes workspaces; Cortex brands its own dir and still git-ignores it.
const StateDir = ".cortex"

// Config holds resolved runtime policy for a kernel instance.
type Config struct {
	// Workspace is the absolute path to the repository/working directory.
	Workspace string
	// CasesDir is where case files are written. Default is the central XDG tree
	// $XDG_STATE_HOME/cortex/sessions/<repo-slug> (or a pre-existing repo-local
	// .cortex/cases). Override with cases_dir in cortex.yaml or CORTEX_CASES_DIR
	// (absolute paths allowed) to pin sessions anywhere.
	CasesDir string
	// Budget bounds tool use per workflow.
	Budget domain.Budget
	// RedactLiterals are extra exact strings to always mask (e.g. known secret
	// names surfaced by tvault). Never populate this with secret values.
	RedactLiterals []string
	// Recall configures cross-case disproof recall, the fourth memory layer.
	// Defaults: a central veclite DB, the nomic-embed-text model,
	// ollama at localhost:11434, enabled.
	Recall RecallConfig
	// Verifiers are repository-defined, read-only command checks. The command is
	// an argv array rather than a shell string, so Cortex never evaluates shell
	// metacharacters supplied through MCP/CLI input. Only checks declared in
	// cortex.yaml can run.
	Verifiers map[string]CommandVerifier
	// sources records which config files were applied (increasing precedence).
	sources []string
	// problems records invalid on-disk configuration discovered while layering.
	// Config.For remains a value-returning API; Kernel.New calls Validate and
	// refuses to start rather than silently weakening a verification policy.
	problems []error
}

// CommandVerifier is one configured test/build/lint command. Surface is kept
// explicit even though v0.1 accepts only code: a unit test must never silently
// satisfy a browser or terminal claim.
type CommandVerifier struct {
	Argv    []string            `json:"argv"`
	Kind    domain.EvidenceKind `json:"kind"`
	Surface domain.Surface      `json:"surface"`
	Timeout time.Duration       `json:"timeout"`
}

// Validate rejects malformed configuration. A safety kernel must fail closed:
// a misspelled verifier kind cannot degrade into a generic code pass.
func (c Config) Validate() error {
	problems := append([]error(nil), c.problems...)
	if strings.TrimSpace(c.CasesDir) == "" {
		problems = append(problems, errors.New("cases_dir must not be empty"))
	} else if samePath(c.CasesDir, c.Workspace) {
		problems = append(problems, errors.New("cases_dir must not be the workspace root"))
	}
	if c.Recall.Enabled {
		if err := validateRecallEndpoint(c.Recall.EmbedURL, c.Recall.AllowRemote); err != nil {
			problems = append(problems, err)
		}
	}
	for _, budget := range []struct {
		name  string
		value int
		zero  bool
	}{
		{"max_parallel_calls", c.Budget.MaxParallelCalls, false},
		{"max_investigation_rounds", c.Budget.MaxInvestigationRounds, false},
		{"max_raw_output_bytes_per_tool", c.Budget.MaxRawOutputBytesPerTool, false},
		{"max_evidence_items_returned", c.Budget.MaxEvidenceItemsReturned, false},
		{"max_candidate_files_returned", c.Budget.MaxCandidateFilesReturned, false},
		{"max_auto_retries_per_tool", c.Budget.MaxAutoRetriesPerTool, true},
	} {
		if budget.value < 0 || (!budget.zero && budget.value == 0) {
			qualifier := "positive"
			if budget.zero {
				qualifier = "non-negative"
			}
			problems = append(problems, fmt.Errorf("budget %s must be %s", budget.name, qualifier))
		}
	}
	for name, v := range c.Verifiers {
		if !validVerifierName(name) {
			problems = append(problems, fmt.Errorf("command verifier name %q must contain only letters, digits, dash, or underscore", name))
		}
		if len(v.Argv) == 0 || strings.TrimSpace(v.Argv[0]) == "" {
			problems = append(problems, fmt.Errorf("command verifier %q has no executable argv", name))
		}
		for _, arg := range v.Argv {
			if strings.ContainsRune(arg, '\x00') {
				problems = append(problems, fmt.Errorf("command verifier %q contains a NUL byte", name))
				break
			}
		}
		switch v.Kind {
		case domain.KindUnitTest, domain.KindBuild, domain.KindLint:
		default:
			problems = append(problems, fmt.Errorf("command verifier %q kind must be unit_test, build, or lint", name))
		}
		if v.Surface != domain.SurfaceCode {
			problems = append(problems, fmt.Errorf("command verifier %q surface must be code", name))
		}
		if v.Timeout <= 0 {
			problems = append(problems, fmt.Errorf("command verifier %q timeout must be positive", name))
		}
	}
	return errors.Join(problems...)
}

func samePath(a, b string) bool {
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return "", err
		}
		if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
			return resolved, nil
		}
		return absolute, nil
	}
	pathA, errA := canonical(a)
	pathB, errB := canonical(b)
	return errA == nil && errB == nil && pathA == pathB
}

func validateRecallEndpoint(raw string, allowRemote bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("recall embed_url must be an absolute http or https URL")
	}
	if u.User != nil {
		return errors.New("recall embed_url must not contain credentials")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback && !allowRemote {
		return errors.New("remote recall embed_url requires CORTEX_APPROVE_REMOTE_RECALL=1 from the launching environment")
	}
	return nil
}

func validVerifierName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// For resolves configuration for a given workspace directory: built-in defaults
// layered with any cortex.yaml files and CORTEX_* env overrides. A
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
		CasesDir:  DefaultCasesDir(ws),
		Budget:    domain.DefaultBudget(),
		Recall:    DefaultRecall(),
		Verifiers: map[string]CommandVerifier{},
	}
	load(&cfg)
	return cfg
}

// DefaultCasesDir is the built-in case-file location for a workspace. It honors
// a pre-existing repo-local .cortex/cases (so upgrading doesn't strand active
// work), otherwise returns the central XDG location
// $XDG_STATE_HOME/cortex/sessions/<repo-slug>. Prefer For(ws).CasesDir after
// overrides are applied.
func DefaultCasesDir(workspace string) string {
	if local := filepath.Join(workspace, StateDir, "cases"); isDir(local) {
		return local
	}
	return filepath.Join(SessionsRoot(), Slug(workspace))
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// EnsureStateIgnored writes <workspace>/.cortex/.gitignore only when the case
// store is .cortex itself or one of its descendants. It never places a catch-all
// ignore in an arbitrary custom parent, which could hide unrelated project
// files. Best effort — failures are silent. This is the single implementation
// shared by the kernel and the eval harness.
func EnsureStateIgnored(workspace, casesDir string) {
	if casesDir == "" {
		return
	}
	ws := filepath.Clean(workspace)
	cd := filepath.Clean(casesDir)
	stateRoot := filepath.Join(ws, StateDir)
	rel, err := filepath.Rel(stateRoot, cd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	if info, statErr := os.Lstat(stateRoot); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return
	}
	gi := filepath.Join(stateRoot, ".gitignore")
	if _, err := os.Stat(gi); err == nil {
		return
	}
	if err := os.MkdirAll(stateRoot, 0o755); err != nil { // #nosec G301 -- state root holds owner-only case files; the directory entry itself is not secret
		return
	}
	// #nosec G306 -- .gitignore is not secret; world-readable by design.
	_ = os.WriteFile(gi, []byte("# Cortex local state — not source. Ignore everything here.\n*\n"), 0o644)
}

// Home returns the global Cortex config directory. Back-compat shim over
// ConfigDir: $CORTEX_HOME (or a legacy ~/.cortex) still wins, but a fresh
// install now resolves $XDG_CONFIG_HOME/cortex (paths.go).
func Home() string { return ConfigDir() }

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
