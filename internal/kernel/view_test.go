package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestShowSession(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "zeta")
	env, err := kernelAt(t, ws).StartTask(context.Background(), StartInput{Goal: "inspect me"})
	if err != nil || !env.OK {
		t.Fatalf("start: %v %s", err, env.Error)
	}

	v, err := ShowSession(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Case == nil || v.Case.Goal != "inspect me" {
		t.Fatalf("case not loaded: %+v", v.Case)
	}
	if v.Slug != "zeta" {
		t.Errorf("slug = %s, want zeta", v.Slug)
	}
	if len(v.Timeline) == 0 {
		t.Error("expected timeline entries")
	}
	if len(v.PhaseDurations) == 0 || v.ElapsedMs <= 0 {
		t.Errorf("expected phase durations + positive elapsed, got %d durs / %dms", len(v.PhaseDurations), v.ElapsedMs)
	}
}

func TestShowSessionRedactsLegacyRecordsAtProjectionBoundary(t *testing.T) {
	const secret = "ghp_16C7e42F292c6912E7710c838347Ae178B4a99"
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "legacy-redaction")
	k := kernelAt(t, ws)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "safe"})
	c, _ := k.Store().Load(started.TaskID)
	c.Goal = "legacy " + secret
	if err := k.Store().Save(c); err != nil {
		t.Fatal(err)
	}
	if err := k.Store().AppendVerification(c.ID, domain.VerificationRecord{
		ID: "vr_legacy", Claim: "legacy " + secret, Status: domain.VerifyFailed, Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	view, err := ShowSession(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(view)
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), "redacted") {
		t.Fatalf("canonical projection leaked legacy secret: %s", data)
	}
}

func TestShowSessionNotFound(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	if _, err := ShowSession("task_does_not_exist"); err == nil {
		t.Error("expected an error for an unknown session")
	}
}

func TestShowSessionBoundsAutoRefreshingLedgersAndReportsTotals(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := repoNamed(t, "bounded-show")
	k := kernelAt(t, ws)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "keep studio bounded"})
	before, _ := k.Store().Evidence(started.TaskID)
	for i := 0; i < maxSessionViewLedgerRecords+5; i++ {
		if err := k.Store().AppendEvidence(started.TaskID, domain.Evidence{
			ID: fmt.Sprintf("ev_view_%03d", i), Timestamp: time.Now().UTC(), Kind: domain.KindHumanReport,
			Source: domain.Source{Origin: "human"}, Claim: fmt.Sprintf("view fact %d", i),
			Confidence: domain.ConfidenceMedium,
		}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := ShowSession(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	wantTotal := len(before) + maxSessionViewLedgerRecords + 5
	if len(view.Evidence) != maxSessionViewLedgerRecords || view.EvidenceTotal != wantTotal || len(view.ProjectionWarnings) == 0 {
		t.Fatalf("bounded show = retained %d total %d warnings %v, want %d/%d", len(view.Evidence), view.EvidenceTotal, view.ProjectionWarnings, maxSessionViewLedgerRecords, wantTotal)
	}
}
