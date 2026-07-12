package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestOrientationConflictDoesNotAppendPhantomPhaseTransitions(t *testing.T) {
	workspace := testRepo(t)
	k := newTestKernel(t, workspace)
	c := &domain.CaseFile{
		SchemaVersion: domain.SchemaVersion, ID: "task_orientation_conflict", CreatedAt: time.Now().UTC(),
		Goal: "recover orientation", Mode: domain.ModeChange, Status: domain.PhaseNew, Risk: "medium",
		Surfaces: []domain.Surface{domain.SurfaceCode}, Workspace: domain.Workspace{Root: workspace, Repository: "repo"},
	}
	if err := k.Store().Create(c); err != nil {
		t.Fatal(err)
	}
	stale, _ := k.Store().Load(c.ID)
	newer, _ := k.Store().Load(c.ID)
	newer.Notes = append(newer.Notes, "concurrent update")
	if err := k.Store().Save(newer); err != nil {
		t.Fatal(err)
	}

	if got, err := k.finishOrientation(context.Background(), stale, true); err == nil || got.OK {
		t.Fatalf("stale orientation unexpectedly committed: %+v (%v)", got, err)
	}
	events, err := k.Store().PhaseEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("failed orientation appended phantom transitions: %+v", events)
	}

	latest, _ := k.Store().Load(c.ID)
	if got, err := k.finishOrientation(context.Background(), latest, true); err != nil || !got.OK {
		t.Fatalf("orientation retry failed: %+v (%v)", got, err)
	}
	events, _ = k.Store().PhaseEvents(c.ID)
	if len(events) != 2 || events[0].From != domain.PhaseNew || events[0].To != domain.PhaseOrienting ||
		events[1].From != domain.PhaseOrienting || events[1].To != domain.PhaseInvestigating {
		t.Fatalf("orientation retry did not emit each committed transition once: %+v", events)
	}
	evidence, err := k.Store().Evidence(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	orientationFacts := 0
	for _, item := range evidence {
		if item.ID == "ev_orientation_git" {
			orientationFacts++
		}
	}
	if orientationFacts != 1 {
		t.Fatalf("orientation recovery duplicated baseline evidence: %+v", evidence)
	}
}

func TestRememberWriteFailureDoesNotAppendPhantomPhaseTransitions(t *testing.T) {
	workspace := testRepo(t)
	k := newTestKernel(t, workspace)
	c := &domain.CaseFile{
		SchemaVersion: domain.SchemaVersion, ID: "task_remember_failure", CreatedAt: time.Now().UTC(),
		Goal: "preserve outcome", Mode: domain.ModeInvestigate, Status: domain.PhaseVerifying, Risk: "low",
		Surfaces: []domain.Surface{domain.SurfaceCode}, Workspace: domain.Workspace{Root: workspace, Repository: "repo"},
	}
	if err := k.Store().Create(c); err != nil {
		t.Fatal(err)
	}
	if err := k.Store().AppendVerification(c.ID, domain.VerificationRecord{
		ID: "vr_remember_pass", Claim: "investigation verified", Surface: domain.SurfaceCode,
		Purpose: domain.VerificationPurposeVerifierRun, Tool: "codemap", Status: domain.VerifyPassed,
		Binding: domain.VerificationBound,
	}); err != nil {
		t.Fatal(err)
	}
	taskDir, err := k.Store().TaskDir(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(taskDir, "summary.md")
	if err := os.Mkdir(summaryPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if got, err := k.Remember(context.Background(), RememberInput{TaskID: c.ID, Outcome: "done"}); err == nil || got.OK {
		t.Fatalf("remember unexpectedly succeeded with an unwritable summary: %+v (%v)", got, err)
	}
	latest, _ := k.Store().Load(c.ID)
	if latest.Status != domain.PhaseVerifying {
		t.Fatalf("failed remember persisted phase %q", latest.Status)
	}
	events, _ := k.Store().PhaseEvents(c.ID)
	if len(events) != 0 {
		t.Fatalf("failed remember appended phantom transitions: %+v", events)
	}

	if err := os.Remove(summaryPath); err != nil {
		t.Fatal(err)
	}
	if got, err := k.Remember(context.Background(), RememberInput{TaskID: c.ID, Outcome: "done"}); err != nil || !got.OK {
		t.Fatalf("remember retry failed: %+v (%v)", got, err)
	}
	events, _ = k.Store().PhaseEvents(c.ID)
	if len(events) != 2 || events[0].From != domain.PhaseVerifying || events[0].To != domain.PhasePersisting ||
		events[1].From != domain.PhasePersisting || events[1].To != domain.PhaseComplete {
		t.Fatalf("remember retry did not emit each committed transition once: %+v", events)
	}
}
