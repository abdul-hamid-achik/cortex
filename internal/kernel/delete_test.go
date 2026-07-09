package kernel

import (
	"context"
	"os"
	"testing"
)

func TestDeleteSessionRefusesInFlight(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "delta")
	k := kernelAt(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "delete me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}
	id := env.TaskID

	if _, err := DeleteSession(id, true); err == nil {
		t.Fatal("expected refusal to delete an in-flight session")
	}
	// Still there.
	if _, err := k.store.Load(id); err != nil {
		t.Errorf("in-flight session should still load after refused delete: %v", err)
	}
}

func TestDeleteSessionDryRun(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "epsilon")
	k := kernelAt(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "delete me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}
	id := env.TaskID
	if _, err := k.AbortTask(id, "no longer needed"); err != nil {
		t.Fatalf("abort: %v", err)
	}

	path, err := DeleteSession(id, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if path == "" {
		t.Fatal("expected a non-empty path from a dry run")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("dry run must not remove the directory: %v", err)
	}
	if _, _, err := LocateSession(id); err != nil {
		t.Errorf("session should still be locatable after a dry run: %v", err)
	}
}

func TestDeleteSessionApply(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "zeta")
	k := kernelAt(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "delete me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}
	id := env.TaskID
	if _, err := k.AbortTask(id, "no longer needed"); err != nil {
		t.Fatalf("abort: %v", err)
	}

	path, err := DeleteSession(id, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if path == "" {
		t.Fatal("expected a non-empty path")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("directory should be gone after apply, stat err: %v", err)
	}
	if _, _, err := LocateSession(id); err == nil {
		t.Error("deleted session should no longer be locatable")
	}
}

func TestDeleteSessionArchived(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "eta")
	k := kernelAt(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "delete me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}
	id := env.TaskID
	if _, err := k.AbortTask(id, "no longer needed"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if _, err := ArchiveSession(id); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := DeleteSession(id, true); err != nil {
		t.Fatalf("delete archived: %v", err)
	}
	arch, _ := ArchivedSessions(SessionFilter{})
	for _, s := range arch {
		if s.ID == id {
			t.Error("deleted session should no longer appear in the archive")
		}
	}
}

func TestDeleteSessionNotFound(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	if _, err := DeleteSession("task_missing", true); err == nil {
		t.Error("expected error deleting an unknown session")
	}
}
