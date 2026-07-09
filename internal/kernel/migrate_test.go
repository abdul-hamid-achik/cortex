package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

// clearMigrateEnv isolates the migrate tests from whatever real environment
// (or other tests running in the same process) might have set: every
// Cortex path-override env var is cleared, then callers point HOME/XDG_* at
// temp dirs of their own choosing.
func clearMigrateEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"CORTEX_HOME", "CORTEX_CONFIG_DIR", "CORTEX_STATE_DIR", "CORTEX_CACHE_DIR",
	} {
		t.Setenv(v, "")
	}
}

// writeDummy creates path (and its parent dirs) with some placeholder content.
func writeDummy(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s to NOT exist", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("stat %s: unexpected error %v", path, err)
	}
}

// TestMigrateRoundTrip is the full dry-run-then-apply lifecycle: a legacy
// ~/.cortex tree with a config.yaml and a nested session case file, first
// planned (dry run — nothing touched), then actually moved into three
// distinct XDG roots, with the now-empty legacy base removed.
func TestMigrateRoundTrip(t *testing.T) {
	clearMigrateEnv(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	xdgConfig := t.TempDir()
	xdgState := t.TempDir()
	xdgCache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("XDG_CACHE_HOME", xdgCache)

	legacyBase := filepath.Join(home, ".cortex")
	legacyConfig := filepath.Join(legacyBase, "config.yaml")
	legacyCase := filepath.Join(legacyBase, "sessions", "repo", "task_x", "case.json")
	writeDummy(t, legacyConfig, "cases_dir: /tmp/whatever\n")
	writeDummy(t, legacyCase, `{"id":"task_x"}`)

	wantConfig := filepath.Join(xdgConfig, "cortex", "config.yaml")
	wantCase := filepath.Join(xdgState, "cortex", "sessions", "repo", "task_x", "case.json")

	// --- dry run: report the moves, touch nothing. ---
	rep, err := Migrate(false)
	if err != nil {
		t.Fatalf("dry-run Migrate: %v", err)
	}
	if rep.Applied {
		t.Error("dry run should report Applied=false")
	}
	if len(rep.Moves) == 0 {
		t.Fatal("dry run should report at least one planned move")
	}
	if rep.Base != legacyBase {
		t.Errorf("Base = %s, want %s", rep.Base, legacyBase)
	}

	// Nothing actually moved yet.
	mustExist(t, legacyConfig)
	mustExist(t, legacyCase)
	mustNotExist(t, wantConfig)
	mustNotExist(t, wantCase)

	// --- apply: perform the moves for real. ---
	rep, err = Migrate(true)
	if err != nil {
		t.Fatalf("apply Migrate: %v", err)
	}
	if !rep.Applied {
		t.Error("apply should report Applied=true")
	}

	mustExist(t, wantConfig)
	mustExist(t, wantCase)
	mustNotExist(t, legacyConfig)
	mustNotExist(t, legacyCase)

	if !rep.RemovedBase {
		t.Error("expected RemovedBase=true once the legacy base is empty")
	}
	mustNotExist(t, legacyBase)

	// Content made the trip intact.
	got, err := os.ReadFile(wantCase)
	if err != nil {
		t.Fatalf("read migrated case file: %v", err)
	}
	if string(got) != `{"id":"task_x"}` {
		t.Errorf("migrated case content = %q, want the original", got)
	}
}

// TestMigrateDryRunDoesNotTouchDisk is a narrower check than the round trip:
// even with several entries (including the special-cased "cache" dir), a dry
// run must leave the legacy tree byte-for-byte alone.
func TestMigrateDryRunDoesNotTouchDisk(t *testing.T) {
	clearMigrateEnv(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	legacyBase := filepath.Join(home, ".cortex")
	writeDummy(t, filepath.Join(legacyBase, "config.yaml"), "x: 1\n")
	writeDummy(t, filepath.Join(legacyBase, "cache", "blobs", "a"), "cached")
	writeDummy(t, filepath.Join(legacyBase, "archive", "repo", "task_y", "case.json"), "{}")

	rep, err := Migrate(false)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if rep.Applied {
		t.Error("Applied should be false on a dry run")
	}
	if len(rep.Moves) != 3 {
		t.Fatalf("expected 3 planned moves, got %d: %+v", len(rep.Moves), rep.Moves)
	}
	// Legacy base must still be fully intact.
	mustExist(t, filepath.Join(legacyBase, "config.yaml"))
	mustExist(t, filepath.Join(legacyBase, "cache", "blobs", "a"))
	mustExist(t, filepath.Join(legacyBase, "archive", "repo", "task_y", "case.json"))
}

// TestMigrateBlocksOnConflict is the fix for the review finding: if ANY XDG
// destination already exists, migrate is all-or-nothing — it moves NOTHING
// (not even the non-conflicting entries), so it can never leave a half-migrated
// state where moved sessions are stranded/invisible under the surviving legacy
// base. It must not clobber the existing destination either.
func TestMigrateBlocksOnConflict(t *testing.T) {
	clearMigrateEnv(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	xdgConfig := t.TempDir()
	xdgState := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	legacyBase := filepath.Join(home, ".cortex")
	legacyConfig := filepath.Join(legacyBase, "config.yaml")
	legacyCase := filepath.Join(legacyBase, "sessions", "repo", "task_x", "case.json")
	writeDummy(t, legacyConfig, "legacy: true\n")
	writeDummy(t, legacyCase, `{"id":"task_x"}`) // NOT a conflict on its own

	existingConfig := filepath.Join(xdgConfig, "cortex", "config.yaml")
	writeDummy(t, existingConfig, "already: here\n") // the conflict

	rep, err := Migrate(true)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Blocked: nothing moved, a Note explains why, and the conflict is flagged.
	if rep.Applied {
		t.Error("a conflict must block the migration (Applied should be false)")
	}
	if rep.Note == "" {
		t.Error("expected a Note explaining the migration was blocked by a conflict")
	}
	sawConflict := false
	for _, mv := range rep.Moves {
		if mv.Skipped != "" {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Errorf("expected the config.yaml conflict to be flagged, got %+v", rep.Moves)
	}

	// Crucially: the NON-conflicting session was NOT moved (no stranding).
	mustExist(t, legacyConfig)
	mustExist(t, legacyCase)
	mustNotExist(t, filepath.Join(xdgState, "cortex", "sessions"))
	// The existing destination keeps its own content, and the base survives.
	got, err := os.ReadFile(existingConfig)
	if err != nil {
		t.Fatalf("read existing destination: %v", err)
	}
	if string(got) != "already: here\n" {
		t.Errorf("existing destination was overwritten: %q", got)
	}
	if rep.RemovedBase {
		t.Error("RemovedBase should be false when the migration is blocked")
	}
	mustExist(t, legacyBase)
}

// TestMigrateCortexHomeSet covers the "nothing to do" branch: with CORTEX_HOME
// pinned, migrate must not plan or perform any move and must say why.
func TestMigrateCortexHomeSet(t *testing.T) {
	clearMigrateEnv(t)
	t.Setenv("CORTEX_HOME", t.TempDir())

	rep, err := Migrate(false)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if rep.Note == "" {
		t.Error("expected a Note explaining CORTEX_HOME is set")
	}
	if rep.Applied || len(rep.Moves) != 0 {
		t.Errorf("expected no moves and Applied=false, got %+v", rep)
	}

	rep, err = Migrate(true)
	if err != nil {
		t.Fatalf("Migrate(apply=true): %v", err)
	}
	if rep.Note == "" || rep.Applied || len(rep.Moves) != 0 {
		t.Errorf("apply=true with CORTEX_HOME set should still be a no-op, got %+v", rep)
	}
}

// TestMigrateNoLegacyBase covers the "already on XDG" branch: no ~/.cortex
// directory at all.
func TestMigrateNoLegacyBase(t *testing.T) {
	clearMigrateEnv(t)
	t.Setenv("HOME", t.TempDir())

	rep, err := Migrate(false)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if rep.Note == "" {
		t.Error("expected a Note explaining there's nothing to migrate")
	}
	if rep.Applied || len(rep.Moves) != 0 {
		t.Errorf("expected no moves and Applied=false, got %+v", rep)
	}
}
