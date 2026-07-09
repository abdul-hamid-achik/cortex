package config

import (
	"path/filepath"
	"testing"
)

func TestBaseOverrideCollapsesDirs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CORTEX_HOME", base)
	// Clear per-dir overrides so the base wins.
	t.Setenv("CORTEX_CONFIG_DIR", "")
	t.Setenv("CORTEX_STATE_DIR", "")
	t.Setenv("CORTEX_CACHE_DIR", "")
	if got := ConfigDir(); got != base {
		t.Errorf("ConfigDir = %s, want %s", got, base)
	}
	if got := StateHome(); got != base {
		t.Errorf("StateHome = %s, want %s", got, base)
	}
	if got := CacheHome(); got != filepath.Join(base, "cache") {
		t.Errorf("CacheHome = %s, want %s", got, filepath.Join(base, "cache"))
	}
	if got := SessionsRoot(); got != filepath.Join(base, "sessions") {
		t.Errorf("SessionsRoot = %s, want %s", got, filepath.Join(base, "sessions"))
	}
	if got := ArchiveRoot(); got != filepath.Join(base, "archive") {
		t.Errorf("ArchiveRoot = %s, want %s", got, filepath.Join(base, "archive"))
	}
}

func TestPerDirOverrideBeatsBase(t *testing.T) {
	base := t.TempDir()
	state := t.TempDir()
	t.Setenv("CORTEX_HOME", base)
	t.Setenv("CORTEX_STATE_DIR", state)
	if got := StateHome(); got != state {
		t.Errorf("CORTEX_STATE_DIR should win: StateHome = %s, want %s", got, state)
	}
	if got := ConfigDir(); got != base {
		t.Errorf("ConfigDir should still be base: got %s, want %s", got, base)
	}
}

func TestXDGSplitWhenNoBase(t *testing.T) {
	// A HOME without ~/.cortex, no CORTEX_HOME → the split XDG layout.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CORTEX_HOME", "")
	t.Setenv("CORTEX_CONFIG_DIR", "")
	t.Setenv("CORTEX_STATE_DIR", "")
	cfgHome := t.TempDir()
	stateHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	if got, want := ConfigDir(), filepath.Join(cfgHome, "cortex"); got != want {
		t.Errorf("ConfigDir = %s, want %s", got, want)
	}
	if got, want := StateHome(), filepath.Join(stateHome, "cortex"); got != want {
		t.Errorf("StateHome = %s, want %s", got, want)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"/home/u/projects/cortex": "cortex",
		"/home/u/my.app":          "my.app",
		"/home/u/weird @name":     "weird--name",
		"/home/u/-hidden-":        "hidden",
		"/":                       "workspace",
		"":                        "workspace",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
