package kernel

import (
	"context"
	"reflect"
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

// TestRecordObservationInvalidEnumOffersFixedVocabularyAsCandidates covers the
// note rejection path: an invalid category/origin/confidence should return a
// retryable cortex_note continuation naming the exact bad field, with the
// same fixed vocabulary the prose already names available as Candidates.
func TestRecordObservationInvalidEnumOffersFixedVocabularyAsCandidates(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})

	badCategory, _ := k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: "x", Category: "musing"})
	if badCategory.OK {
		t.Fatal("invalid category accepted")
	}
	action := findAction(badCategory.Actions, "cortex_note")
	if action == nil {
		t.Fatalf("expected a cortex_note continuation, got actions=%+v", badCategory.Actions)
	}
	if len(action.Inputs) != 1 || action.Inputs[0] != "category" {
		t.Fatalf("continuation inputs = %v, want [category]", action.Inputs)
	}
	want := []string{"observation", "decision", "constraint", "handoff"}
	if !reflect.DeepEqual(action.Candidates["category"], want) {
		t.Fatalf("category candidates = %v, want %v", action.Candidates["category"], want)
	}
	if action.Arguments["taskId"] != started.TaskID {
		t.Fatalf("continuation dropped the known taskId: %+v", action.Arguments)
	}

	badOrigin, _ := k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: "x", Origin: "ghost"})
	if badOrigin.OK {
		t.Fatal("invalid origin accepted")
	}
	originAction := findAction(badOrigin.Actions, "cortex_note")
	if originAction == nil || !reflect.DeepEqual(originAction.Candidates["origin"], []string{"human", "agent", "reviewer"}) {
		t.Fatalf("expected origin candidates, got %+v", badOrigin.Actions)
	}
}
