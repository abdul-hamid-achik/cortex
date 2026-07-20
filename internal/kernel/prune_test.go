package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func startPruneSession(t *testing.T, goal string) string {
	t.Helper()
	k := newTestKernel(t, testRepo(t))
	env, err := k.StartTask(context.Background(), StartInput{Goal: goal, Surfaces: []domain.Surface{domain.SurfaceCode}})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %+v", err, env)
	}
	return env.TaskID
}

func sessionIDs(ss []SessionSummary) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestPruneStaleDryRunReportsWithoutChanging(t *testing.T) {
	taskID := startPruneSession(t, "forgotten work")
	future := time.Now().Add(30 * 24 * time.Hour)

	rep, err := PruneStale(future, 7*24*time.Hour, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(sessionIDs(rep.Stale), taskID) {
		t.Fatalf("dry run should list the stale session, got %+v", rep.Stale)
	}
	if rep.Applied || len(rep.Pruned) != 0 {
		t.Fatalf("dry run must not prune: %+v", rep)
	}
	active, _ := AllSessions(SessionFilter{ActiveOnly: true})
	if !containsID(sessionIDs(active), taskID) {
		t.Fatal("dry run should leave the session active")
	}
}

func TestPruneStaleApplyArchivesForgottenSessions(t *testing.T) {
	taskID := startPruneSession(t, "forgotten work")
	future := time.Now().Add(30 * 24 * time.Hour)

	rep, err := PruneStale(future, 7*24*time.Hour, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(rep.Pruned, taskID) {
		t.Fatalf("apply should prune the stale session, got %+v", rep)
	}
	active, _ := AllSessions(SessionFilter{ActiveOnly: true})
	if containsID(sessionIDs(active), taskID) {
		t.Fatal("pruned session should no longer be active")
	}
	archived, _ := ArchivedSessions(SessionFilter{})
	if !containsID(sessionIDs(archived), taskID) {
		t.Fatal("pruned session should be archived")
	}
}

func TestPruneStaleSkipsFreshSessions(t *testing.T) {
	taskID := startPruneSession(t, "active work")
	rep, err := PruneStale(time.Now(), 7*24*time.Hour, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if containsID(sessionIDs(rep.Stale), taskID) || containsID(rep.Pruned, taskID) {
		t.Fatalf("a freshly updated session must not be pruned: %+v", rep)
	}
}

func TestFormatAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		7 * 24 * time.Hour: "7d",
		48 * time.Hour:     "2d",
		36 * time.Hour:     "36h",
		5 * time.Hour:      "5h",
		90 * time.Minute:   "1h30m0s",
	} {
		if got := FormatAge(d); got != want {
			t.Errorf("FormatAge(%v) = %q, want %q", d, got, want)
		}
	}
}
