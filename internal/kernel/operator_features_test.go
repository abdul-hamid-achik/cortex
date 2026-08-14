package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestVerifyFromPlanUsesAcceptanceCriteria(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "1")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
		Facts: []adapters.Fact{{Kind: "unit_test", Claim: "tests passed", Confidence: "high"}},
	}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "change callback", Risk: "low",
		AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "unit_ok", Statement: "unit suite passes"}},
	})
	if _, err := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback needs a change", DisproveBy: "unit tests fail"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"command:unit"}, Uncertainty: "coverage",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, err := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, FromPlan: true})
	if err != nil || !verified.OK {
		t.Fatalf("from-plan verify: %+v %v", verified, err)
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	found := false
	for _, receipt := range receipts {
		if receipt.ClaimID == "unit_ok" && receipt.Claim == "unit suite passes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected named claim for acceptance criterion, receipts=%+v", receipts)
	}
}

func TestHighRiskScopeDriftBlocksVerify(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "risky change", Risk: "high"})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback is wrong", DisproveBy: "tests fail"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "other files may be involved",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "other.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if blocked.OK {
		t.Fatalf("high-risk drift should block verify: %+v", blocked)
	}
	if !strings.Contains(blocked.Error, "scope drift") {
		t.Fatalf("error should name scope drift: %s", blocked.Error)
	}
	acked, err := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, DriftAcknowledged: true})
	if err != nil || !acked.OK {
		t.Fatalf("acked drift should proceed: %+v %v", acked, err)
	}
}

func TestCommandGrantAllowsConfiguredVerifier(t *testing.T) {
	t.Setenv("CORTEX_APPROVE_COMMANDS", "")
	ws := testRepo(t)
	command := &fakeAdapter{name: "command", result: adapters.Result{
		Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed,
		Facts: []adapters.Fact{{Kind: "unit_test", Claim: "tests passed", Confidence: "high"}},
	}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	if _, err := k.TrustCommandVerifiers(); err != nil {
		t.Fatal(err)
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "granted argv", Risk: "low"})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "tests should pass", DisproveBy: "unit fails"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"command:unit"}, Uncertainty: "u",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 9 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !verified.OK {
		t.Fatalf("granted verifier should run: %+v", verified)
	}
	if len(command.requests()) == 0 {
		t.Fatal("granted command verifier was not executed")
	}
}

func TestRememberRefusesOpenChildren(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	parent, _ := k.StartTask(context.Background(), StartInput{Goal: "parent work", Risk: "low"})
	child, _ := k.StartTask(context.Background(), StartInput{Goal: "child work", Risk: "low", ParentTaskID: parent.TaskID})
	if child.OK != true {
		t.Fatalf("child start: %+v", child)
	}
	_, _ = k.Plan(PlanInput{
		TaskID:         parent.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "parent hypothesis", DisproveBy: "inspect"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u",
	})
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: parent.TaskID, NoOpAcknowledged: true})
	blocked, _ := k.Remember(context.Background(), RememberInput{TaskID: parent.TaskID, Outcome: "parent done", VerificationNotPossible: true})
	if blocked.OK {
		t.Fatalf("parent remember should wait on child: %+v", blocked)
	}
	acked, err := k.Remember(context.Background(), RememberInput{
		TaskID: parent.TaskID, Outcome: "parent done", VerificationNotPossible: true, AcceptOpenChildren: true,
	})
	if err != nil || !acked.OK {
		t.Fatalf("accept-open-children: %+v %v", acked, err)
	}
	st, _ := k.Status(context.Background(), parent.TaskID, "standard")
	if len(st.Children) != 1 || st.Children[0].ID != child.TaskID || !st.Children[0].Active {
		t.Fatalf("status children = %+v", st.Children)
	}
}

func TestAllSessionsIncludesKnownCustomStore(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	ws := testRepo(t)
	cases := filepath.Join(t.TempDir(), "custom-cases")
	cfg := config.For(ws)
	cfg.CasesDir = cases
	k, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "custom store task"})
	if !started.OK {
		t.Fatalf("start: %+v", started)
	}
	sessions, err := AllSessions(SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == started.TaskID {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom-store session missing from AllSessions: %+v", sessions)
	}
}

func TestSubQuestionsSplitsSpanishObjectConjunction(t *testing.T) {
	subs := subQuestions("Cómo el ingreso de la cola de trabajos aplica idempotencia y límites de tamaño?", maxSubQueries)
	if len(subs) < 2 {
		t.Fatalf("expected Spanish object split, got %v", subs)
	}
}

func TestGrepPatternsCollectsIdentifiers(t *testing.T) {
	got := grepPatterns("where is HandleCallback and refundToken used")
	if len(got) < 2 || got[0] != "HandleCallback" || got[1] != "refundToken" {
		t.Fatalf("grepPatterns = %v", got)
	}
}
