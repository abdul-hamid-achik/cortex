package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/config"
)

func TestWorkspaceAwareViewsFindRepoLocalCaseAndCarryWorkspaceActions(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	workspace := testRepo(t)
	if err := os.WriteFile(filepath.Join(workspace, "cortex.yaml"), []byte("cases_dir: .cortex/cases\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := New(config.For(workspace))
	if err != nil {
		t.Fatal(err)
	}
	started, err := k.StartTask(context.Background(), StartInput{Goal: "portable local case", Actor: "agent-a"})
	if err != nil || !started.OK {
		t.Fatalf("start: %+v (%v)", started, err)
	}
	if got := k.Store().Root(); got != filepath.Join(workspace, ".cortex", "cases") {
		t.Fatalf("case did not use repo-local store: %s", got)
	}

	view, err := ShowSessionIn(workspace, started.TaskID)
	if err != nil || view.Case.ID != started.TaskID {
		t.Fatalf("workspace-aware show: %+v (%v)", view.Case, err)
	}
	entries, err := TimelineIn(workspace, started.TaskID)
	if err != nil || len(entries) == 0 {
		t.Fatalf("workspace-aware timeline: %d entries (%v)", len(entries), err)
	}
	handoff, err := BuildHandoffIn(workspace, started.TaskID, time.Now())
	if err != nil || handoff.TaskID != started.TaskID || len(handoff.Actions) == 0 {
		t.Fatalf("workspace-aware handoff: %+v (%v)", handoff, err)
	}
	for _, action := range handoff.Actions {
		if action.Arguments["workspace"] != workspace {
			t.Fatalf("handoff action is not workspace-portable: %+v", action)
		}
	}
}
