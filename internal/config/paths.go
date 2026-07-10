package config

import (
	"os"
	"path/filepath"
	"strings"
)

// appName brands Cortex's XDG subdirectories (e.g. $XDG_STATE_HOME/cortex).
const appName = "cortex"

// Path-override environment variables. EnvHome collapses config+state+cache into
// one directory (the single-dir layout the ecosystem uses — vecgrep's
// ~/.vecgrep, codemap's ~/.codemap); the per-dir overrides win over it for their
// own dir when both are set.
const (
	EnvHome      = "CORTEX_HOME"       // override-all base directory
	EnvConfigDir = "CORTEX_CONFIG_DIR" // override the config directory only
	EnvStateDir  = "CORTEX_STATE_DIR"  // override the state directory only
	EnvCacheDir  = "CORTEX_CACHE_DIR"  // override the cache directory only
)

// legacyBase returns ~/.cortex if it already exists as a directory. This keeps
// pre-XDG installs working unchanged: a user who already has ~/.cortex keeps it,
// while a fresh install gets the split XDG layout (matches codemap's legacyBase).
func legacyBase() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	base := filepath.Join(home, "."+appName)
	if fi, err := os.Stat(base); err == nil && fi.IsDir() {
		return base, true
	}
	return "", false
}

// baseOverride returns an explicit whole-tree base and true when one applies:
// $CORTEX_HOME (expanded) if set, else a legacy ~/.cortex if present. When it
// returns false the caller falls through to the per-purpose XDG directory.
func baseOverride() (string, bool) {
	if h := os.Getenv(EnvHome); h != "" {
		return ExpandPath(h), true
	}
	return legacyBase()
}

// xdgDir returns $<envVar>, or $HOME/<fallbackRel> when it is unset. XDG is
// resolved explicitly (not via os.UserConfigDir) so the behavior is identical on
// Linux and macOS — honoring the user's XDG request rather than macOS's
// ~/Library convention.
func xdgDir(envVar, fallbackRel string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallbackRel)
}

// ConfigDir is the directory holding the global config.yaml
// ($XDG_CONFIG_HOME/cortex, or the override/legacy base).
func ConfigDir() string {
	if v := os.Getenv(EnvConfigDir); v != "" {
		return ExpandPath(v)
	}
	if base, ok := baseOverride(); ok {
		return base
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), appName)
}

// StateHome is the root for Cortex's mutable session state: the case files
// (under SessionsRoot) and the global session registry. Cases go in STATE, not
// DATA: a case.json embeds absolute workspace paths and git refs, so it is
// machine-local working state, not portable/backup-worthy data (XDG spec).
func StateHome() string {
	if v := os.Getenv(EnvStateDir); v != "" {
		return ExpandPath(v)
	}
	if base, ok := baseOverride(); ok {
		return base
	}
	return filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), appName)
}

// CacheHome holds derived caches that are safe to delete.
func CacheHome() string {
	if v := os.Getenv(EnvCacheDir); v != "" {
		return ExpandPath(v)
	}
	if base, ok := baseOverride(); ok {
		return filepath.Join(base, "cache")
	}
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), appName)
}

// DataHome holds portable, backup-worthy data. The cross-case recall vector
// index lives here (XDG spec: a generated index is data, not state).
func DataHome() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return ExpandPath(v)
	}
	if base, ok := baseOverride(); ok {
		return filepath.Join(base, "data")
	}
	return filepath.Join(xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share")), appName)
}

// defaultRecallDB is the built-in veclite DB path for cross-case recall.
func defaultRecallDB() string {
	return filepath.Join(DataHome(), "cases.veclite")
}

// RecallConfig configures cross-case disproof recall (SPEC §15.4). Defaults
// are sensible for a local ollama + veclite setup; every field is overridable.
type RecallConfig struct {
	Enabled    bool
	DBPath     string
	EmbedModel string
	EmbedURL   string
}

// DefaultRecall is the built-in recall config: a central veclite DB, the
// nomic-embed-text model, ollama at localhost:11434, enabled.
func DefaultRecall() RecallConfig {
	return RecallConfig{
		Enabled:    true,
		DBPath:     defaultRecallDB(),
		EmbedModel: "nomic-embed-text",
		EmbedURL:   "http://localhost:11434/api/embeddings",
	}
}

// XDGConfigDir is where ConfigDir resolves once no legacy ~/.cortex (and no
// $CORTEX_HOME) applies — i.e. the migration target for config.yaml. Unlike
// ConfigDir it never honors baseOverride's legacy detection, so `cortex
// migrate` can compute "where things go" independent of "where they are now".
func XDGConfigDir() string {
	if v := os.Getenv(EnvConfigDir); v != "" {
		return ExpandPath(v)
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), appName)
}

// XDGStateHome is the migration target for StateHome — the split-layout state
// root, skipping the legacy ~/.cortex base.
func XDGStateHome() string {
	if v := os.Getenv(EnvStateDir); v != "" {
		return ExpandPath(v)
	}
	return filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), appName)
}

// XDGCacheHome is the migration target for CacheHome — the split-layout cache
// root, skipping the legacy ~/.cortex base.
func XDGCacheHome() string {
	if v := os.Getenv(EnvCacheDir); v != "" {
		return ExpandPath(v)
	}
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), appName)
}

// LegacyBase exposes legacyBase to callers outside the package (`cortex
// migrate`): the pre-XDG ~/.cortex directory, if it exists.
func LegacyBase() (string, bool) { return legacyBase() }

// SessionsRoot is the central directory under which every case (session) lives,
// grouped by workspace: <state>/sessions/<slug>/<taskID>/. Walking it yields
// every session across every repo — the substrate for cross-workspace audit.
func SessionsRoot() string { return filepath.Join(StateHome(), "sessions") }

// ArchiveRoot mirrors SessionsRoot for archived (retired) sessions:
// <state>/archive/<slug>/<taskID>/. Archiving MOVES a session here — data is
// preserved and reversible, it's just out of the active audit view.
func ArchiveRoot() string { return filepath.Join(StateHome(), "archive") }

// Slug is the stable, human-readable directory name for a workspace under
// SessionsRoot — the sanitized basename, so `ls <sessions>` reads as a list of
// repos. Two repos sharing a basename collide into one slug on disk; the
// per-workspace views (`cortex list` / `metrics`) then filter by each case's
// recorded Workspace.Root to keep them distinct, so the slug is a convenience
// label, never the identity.
func Slug(workspace string) string {
	base := filepath.Base(filepath.Clean(workspace))
	out := make([]rune, 0, len(base))
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	// Trim leading/trailing separators so a slug can't become a hidden dir.
	if s := strings.Trim(string(out), "-."); s != "" {
		return s
	}
	return "workspace"
}
