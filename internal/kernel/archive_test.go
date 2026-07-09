package kernel

import (
	"context"
	"testing"
)

func TestArchiveRoundTrip(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "omega")
	k := kernelAt(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "archive me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}
	id := env.TaskID

	// In-flight sessions must be refused.
	if _, err := ArchiveSession(id); err == nil {
		t.Fatal("expected refusal to archive an in-flight session")
	}

	// Make it terminal, then archive.
	if _, err := k.AbortTask(id, "done experimenting"); err != nil {
		t.Fatal(err)
	}
	if _, err := ArchiveSession(id); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Gone from the active list, present in the archive.
	active, _ := AllSessions(SessionFilter{})
	for _, s := range active {
		if s.ID == id {
			t.Error("archived session should not appear in AllSessions")
		}
	}
	arch, _ := ArchivedSessions(SessionFilter{})
	if len(arch) != 1 || arch[0].ID != id {
		t.Fatalf("expected the session in the archive, got %+v", arch)
	}
	// It's no longer locatable in the active tree...
	if _, _, err := LocateSession(id); err == nil {
		t.Error("archived session should not be locatable in the active tree")
	}

	// Restore it.
	if _, err := UnarchiveSession(id); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if _, _, err := LocateSession(id); err != nil {
		t.Errorf("restored session should be locatable again: %v", err)
	}
	if arch, _ := ArchivedSessions(SessionFilter{}); len(arch) != 0 {
		t.Errorf("archive should be empty after restore, got %+v", arch)
	}
}

func TestArchiveNotFound(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	if _, err := ArchiveSession("task_missing"); err == nil {
		t.Error("expected error archiving an unknown session")
	}
}
