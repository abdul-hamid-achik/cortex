package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leasedChangingTask plans a task, claims a change lease for owner, and edits a
// boundary file so verify has a real diff to check.
func leasedChangingTask(t *testing.T, owner string) (*Kernel, string) {
	t.Helper()
	k, workspace, taskID := plannedConformanceTask(t, owner)
	if begun, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: owner}); !begun.OK {
		t.Fatalf("begin-change: %+v", begun)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return k, taskID
}

func TestVerifyDefaultsActorToActiveLeaseOwner(t *testing.T) {
	k, taskID := leasedChangingTask(t, "agent-a")
	// No --actor supplied: it should default to the active lease owner rather
	// than fail with "verify must name that actor".
	got, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID})
	if strings.Contains(got.Error, "must name that actor") || strings.Contains(got.Error, "active change lease belongs") {
		t.Fatalf("empty actor should default to the lease owner, got error: %q", got.Error)
	}
	if !got.OK {
		t.Fatalf("verify with a defaulted actor should succeed, got: %+v", got)
	}
}

func TestVerifyStillRejectsExplicitWrongActor(t *testing.T) {
	k, taskID := leasedChangingTask(t, "agent-a")
	// An explicit actor that is not the lease owner must still be rejected.
	got, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, Actor: "agent-b"})
	if got.OK || !strings.Contains(got.Error, "active change lease belongs") {
		t.Fatalf("explicit wrong actor should be rejected, got ok=%v error=%q", got.OK, got.Error)
	}
}
