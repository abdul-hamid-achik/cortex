package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestStartRejectsUnknownLifecycleInputs(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	tests := []struct {
		name  string
		input StartInput
		want  string
	}{
		{name: "mode", input: StartInput{Goal: "g", Mode: domain.Mode("mutate")}, want: "mode must be one of"},
		{name: "risk", input: StartInput{Goal: "g", Risk: "critical"}, want: "risk must be one of"},
		{name: "surface", input: StartInput{Goal: "g", Surfaces: []domain.Surface{"desktop"}}, want: "surface must be one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := k.StartTask(context.Background(), tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.OK || !strings.Contains(got.Error, tt.want) {
				t.Fatalf("invalid %s accepted: ok=%v error=%q", tt.name, got.OK, got.Error)
			}
		})
	}
	ids, err := k.Store().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("invalid start inputs must not create cases, got %v", ids)
	}
}

func TestStartNormalizesValidInputsAndKeepsEmptyDefaults(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	got, _ := k.StartTask(context.Background(), StartInput{
		Goal: "g", Mode: domain.Mode(" REVIEW "), Risk: " HIGH ",
		Surfaces: []domain.Surface{" Browser ", "browser", "CODE"},
	})
	if !got.OK {
		t.Fatalf("normalizable input rejected: %s", got.Error)
	}
	c, _ := k.Store().Load(got.TaskID)
	if c.Mode != domain.ModeReview || c.Risk != "high" {
		t.Fatalf("inputs not normalized: mode=%q risk=%q", c.Mode, c.Risk)
	}
	if len(c.Surfaces) != 2 || c.Surfaces[0] != domain.SurfaceBrowser || c.Surfaces[1] != domain.SurfaceCode {
		t.Fatalf("surfaces not normalized/deduplicated: %v", c.Surfaces)
	}

	defaults, _ := k.StartTask(context.Background(), StartInput{Goal: "defaults"})
	d, _ := k.Store().Load(defaults.TaskID)
	if d.Mode != domain.ModeChange || d.Risk != "medium" || len(d.Surfaces) != 1 || d.Surfaces[0] != domain.SurfaceCode {
		t.Fatalf("empty values did not retain defaults: mode=%q risk=%q surfaces=%v", d.Mode, d.Risk, d.Surfaces)
	}
}

func TestInvestigateRejectsInvalidDepthAndSurfaceBeforeAdapterCalls(t *testing.T) {
	vecgrep := &fakeAdapter{name: "vecgrep", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})

	tests := []struct {
		name  string
		input InvestigateInput
		want  string
	}{
		{
			name: "depth", input: InvestigateInput{TaskID: started.TaskID, Question: "where is it", Depth: "fast"},
			want: "depth must be one of",
		},
		{
			name: "surface", input: InvestigateInput{TaskID: started.TaskID, Question: "where is it", Surfaces: []domain.Surface{"desktop"}},
			want: "surface must be one of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := k.Investigate(context.Background(), tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.OK || !strings.Contains(got.Error, tt.want) {
				t.Fatalf("invalid investigate %s accepted: %+v", tt.name, got)
			}
		})
	}
	if got := len(vecgrep.requests()) + len(codemap.requests()); got != 0 {
		t.Fatalf("invalid investigate inputs launched %d adapter call(s)", got)
	}
	c, err := k.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if c.InvestigationRounds != 0 {
		t.Fatalf("invalid investigate inputs consumed %d investigation round(s)", c.InvestigationRounds)
	}
}

func TestOpenValidatesRiskAndSurfacesBeforeResume(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	first, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "stable work", IdempotencyKey: "stable-open",
	}})
	if err != nil || !first.OK {
		t.Fatalf("first open: %+v (%v)", first, err)
	}

	for _, tc := range []struct {
		name  string
		input StartInput
		want  string
	}{
		{
			name: "risk", input: StartInput{Goal: "retry text", IdempotencyKey: "stable-open", Risk: "critical"},
			want: "risk must be one of",
		},
		{
			name: "surface", input: StartInput{Goal: "retry text", IdempotencyKey: "stable-open", Surfaces: []domain.Surface{"desktop"}},
			want: "surface must be one of",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, callErr := k.OpenTask(context.Background(), OpenInput{StartInput: tc.input})
			if callErr != nil {
				t.Fatal(callErr)
			}
			if got.OK || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("invalid open %s resumed a case: %+v", tc.name, got)
			}
		})
	}
	ids, err := k.Store().List()
	if err != nil || len(ids) != 1 || ids[0] != first.TaskID {
		t.Fatalf("invalid retries changed task identity: ids=%v err=%v first=%s", ids, err, first.TaskID)
	}
}

func TestPlanRejectsUnknownReferencesAndPolicyLabels(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	base := PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "h", DisproveBy: "d",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u",
	}

	missingSupport := base
	missingSupport.Hypotheses = []HypothesisInput{{Statement: "h", DisproveBy: "d", Supports: []string{"ev_missing"}}}
	if got, _ := k.Plan(missingSupport); got.OK || !strings.Contains(got.Error, "not evidence in task") {
		t.Fatalf("missing support ID accepted: ok=%v error=%q", got.OK, got.Error)
	}

	unknownTool := base
	unknownTool.TimeoutOverrides = map[string]string{"mystery": "5s"}
	if got, _ := k.Plan(unknownTool); got.OK || !strings.Contains(got.Error, "unknown timeout override tool") {
		t.Fatalf("unknown timeout tool accepted: ok=%v error=%q", got.OK, got.Error)
	}

	badDuration := base
	badDuration.TimeoutOverrides = map[string]string{"codemap": "0s"}
	if got, _ := k.Plan(badDuration); got.OK || !strings.Contains(got.Error, "positive duration") {
		t.Fatalf("invalid timeout duration accepted: ok=%v error=%q", got.OK, got.Error)
	}

	unknownVerifier := base
	unknownVerifier.Verification = []string{"custom_shell_check"}
	if got, _ := k.Plan(unknownVerifier); got.OK || !strings.Contains(got.Error, "unknown verification requirement") {
		t.Fatalf("unknown verifier accepted: ok=%v error=%q", got.OK, got.Error)
	}

	badConfidence := base
	badConfidence.Hypotheses = []HypothesisInput{{Statement: "h", DisproveBy: "d", Confidence: "meduim"}}
	if got, _ := k.Plan(badConfidence); got.OK || !strings.Contains(got.Error, "confidence must be one of") {
		t.Fatalf("unknown confidence accepted: ok=%v error=%q", got.OK, got.Error)
	}

	symbolOnly := base
	symbolOnly.ChangeBoundary = domain.ChangeBoundary{Symbols: []string{"HandleCallback"}}
	if got, _ := k.Plan(symbolOnly); got.OK || !strings.Contains(got.Error, "symbol-only change boundaries") {
		t.Fatalf("symbol-only boundary accepted: ok=%v error=%q", got.OK, got.Error)
	}

	// A real evidence ID from this task, a known verifier, and a positive timeout
	// remain valid even when that optional tool is currently unavailable.
	evidence, err := k.Store().Evidence(started.TaskID)
	if err != nil || len(evidence) == 0 {
		t.Fatalf("orientation evidence: %v (%d records)", err, len(evidence))
	}
	valid := base
	valid.Hypotheses = []HypothesisInput{{Statement: "h", DisproveBy: "d", Supports: []string{evidence[0].ID}}}
	valid.Verification = []string{"codemap_review"}
	valid.TimeoutOverrides = map[string]string{"codemap": "5s"}
	if got, _ := k.Plan(valid); !got.OK {
		t.Fatalf("valid strict plan rejected: %s", got.Error)
	}
}

func TestStatusRejectsUnknownDetailInsteadOfSilentlyUsingStandard(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "inspect status"})
	report, err := k.Status(context.Background(), started.TaskID, "ful")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || !strings.Contains(report.Error, "standard or full") {
		t.Fatalf("unknown detail accepted: %+v", report)
	}

	normalized, err := k.Status(context.Background(), started.TaskID, " FULL ")
	if err != nil || !normalized.OK {
		t.Fatalf("normalizable full detail rejected: %+v (%v)", normalized, err)
	}
}

func TestDurableTextAndCollectionLimitsFailBeforeWriting(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	if got, _ := k.StartTask(context.Background(), StartInput{Goal: strings.Repeat("g", maxGoalBytes+1)}); got.OK || !strings.Contains(got.Error, "goal exceeds") {
		t.Fatalf("oversized goal accepted: %+v", got)
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "bounded records"})
	before, _ := k.Store().Evidence(started.TaskID)
	if got, _ := k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: strings.Repeat("x", maxRecordTextBytes+1)}); got.OK || !strings.Contains(got.Error, "claim exceeds") {
		t.Fatalf("oversized observation accepted: %+v", got)
	}
	after, _ := k.Store().Evidence(started.TaskID)
	if len(after) != len(before) {
		t.Fatalf("rejected observation wrote evidence: before=%d after=%d", len(before), len(after))
	}
	hypotheses := make([]HypothesisInput, maxPlanHypotheses+1)
	for i := range hypotheses {
		hypotheses[i] = HypothesisInput{Statement: "h", DisproveBy: "d"}
	}
	if got, _ := k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: hypotheses,
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"f.go"}}, Uncertainty: "u",
	}); got.OK || !strings.Contains(got.Error, "more than") {
		t.Fatalf("oversized hypothesis collection accepted: %+v", got)
	}
	options := make([]domain.DecisionOption, maxDecisionOptions+1)
	for i := range options {
		options[i] = domain.DecisionOption{ID: fmt.Sprintf("opt_%d", i), Label: "label", Consequence: "consequence"}
	}
	if got, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: started.TaskID, Question: "choose", Requester: "agent-a", Options: options,
	}); got.OK || !strings.Contains(got.Error, "more than") {
		t.Fatalf("oversized decision collection accepted: %+v", got)
	}
}

func TestReviewRejectsEnumsBeforeResolvingGitScope(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	got, err := k.Review(context.Background(), ReviewInput{Risk: "critical", Surfaces: []domain.Surface{"desktop"}, Head: "missing-ref"})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || (!strings.Contains(got.Error, "surface") && !strings.Contains(got.Error, "risk")) {
		t.Fatalf("invalid review metadata reached scope resolution: %+v", got)
	}
	ids, err := k.Store().List()
	if err != nil || len(ids) != 0 {
		t.Fatalf("invalid review created a case: %v (%v)", ids, err)
	}
}

func TestVerifyGitChangesCannotBeHiddenByCallerHints(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "h", DisproveBy: "d",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "other.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The hint mentions only the planned file. Git still observes other.go, which
	// must survive the merge and trigger drift.
	got, _ := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, ChangedFiles: []string{"src/callback.go"},
	})
	if !got.OK {
		t.Fatalf("verify failed: %s", got.Error)
	}
	if !hasWarning(got.Warnings, "scope drift") {
		t.Fatalf("caller hint hid Git-observed drift: warnings=%v", got.Warnings)
	}
	evidence, _ := k.Store().Evidence(started.TaskID)
	sawOther := false
	for _, item := range evidence {
		if strings.Contains(item.Claim, "src/other.go") {
			sawOther = true
			break
		}
	}
	if !sawOther {
		t.Fatalf("scope evidence omitted Git-observed file: %+v", evidence)
	}
}
