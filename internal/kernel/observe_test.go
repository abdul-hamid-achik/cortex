package kernel

import (
	"context"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestRecordObservationIsRedactedNonVerifyingEvidence(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	res, err := k.RecordObservation(ObservationInput{
		TaskID: started.TaskID, Claim: "API_KEY=supersecretvalue", Category: "constraint",
		Origin: "reviewer", Actor: "alice", Confidence: "medium",
		Location: &domain.Location{File: "TOKEN=supersecretvalue", Symbol: "API_KEY=supersecretvalue"},
	})
	if err != nil || !res.OK || len(res.Facts) != 1 {
		t.Fatalf("record observation = %+v, %v", res, err)
	}
	evidence, _ := k.Store().Evidence(started.TaskID)
	last := evidence[len(evidence)-1]
	if last.Kind != domain.KindHumanReport || last.Kind.CanVerify() {
		t.Fatalf("observation kind must remain non-verifying: %+v", last)
	}
	if strings.Contains(last.Claim, "supersecretvalue") || last.Sensitivity != domain.SensitivitySensitive {
		t.Fatalf("observation was not redacted/sensitive: %+v", last)
	}
	if last.Location == nil || strings.Contains(last.Location.File, "supersecretvalue") || strings.Contains(last.Location.Symbol, "supersecretvalue") {
		t.Fatalf("observation location was not redacted: %+v", last.Location)
	}
}

func TestRecordObservationRejectsHighConfidenceAndTerminalCase(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	bad, _ := k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: "x", Confidence: "high"})
	if bad.OK {
		t.Fatal("prose-only observation must not be high confidence")
	}
	_, _ = k.AbortTask(started.TaskID, "stop")
	terminal, _ := k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: "late"})
	if terminal.OK {
		t.Fatal("terminal case must remain immutable")
	}
}
