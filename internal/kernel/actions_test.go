package kernel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestStructuredActionsPrioritizeRememberOnceVerified(t *testing.T) {
	caseFile := &domain.CaseFile{ID: "task_verified", Status: domain.PhaseVerifying}
	verified := VerificationAssessment{Outcome: VerificationVerified}
	actions := structuredNextForCase(caseFile, verified)
	if len(actions) != 1 || actions[0].Tool != "cortex_remember" {
		t.Fatalf("verified actions = %+v, want remember first and no redundant rerun", actions)
	}

	partial := VerificationAssessment{Outcome: VerificationPartial}
	actions = structuredNextForCase(caseFile, partial)
	if len(actions) < 2 || actions[0].Tool != "cortex_verify" || actions[1].Tool != "cortex_remember" {
		t.Fatalf("partial actions = %+v, want verify before optional preservation", actions)
	}
}

func TestStructuredActionsRepairFailedOrUnownedChange(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	caseFile := &domain.CaseFile{
		ID: "task_repair", Mode: domain.ModeChange, Status: domain.PhaseVerifying,
		Workspace: domain.Workspace{Root: "/tmp/repo"},
		ChangeLease: &domain.ChangeLease{
			Actor: "agent-a", AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
		},
	}
	actions := structuredNextForCaseAt(caseFile, now, VerificationAssessment{Outcome: VerificationFailed})
	if len(actions) != 1 || actions[0].Tool != "cortex_begin_change" || actions[0].Arguments["actor"] != "agent-a" {
		t.Fatalf("failed verification actions = %+v, want owned begin-change repair", actions)
	}
	if actions[0].Arguments["ttl"] != DefaultChangeLeaseTTL.String() || actions[0].Arguments["workspace"] != caseFile.Workspace.Root {
		t.Fatalf("begin-change repair is not fully executable: %+v", actions[0])
	}

	caseFile.Status = domain.PhaseChanging
	caseFile.ChangeLease.ExpiresAt = now
	actions = structuredNextForCaseAt(caseFile, now, VerificationAssessment{Outcome: VerificationUnverified})
	if len(actions) != 1 || actions[0].Tool != "cortex_begin_change" || len(actions[0].Inputs) != 1 || actions[0].Inputs[0] != "actor" {
		t.Fatalf("expired ownership actions = %+v, want actor-bound begin-change", actions)
	}
}

func TestStructuredActionCommandsPinWorkspaceAndQuoteCaseText(t *testing.T) {
	c := &domain.CaseFile{
		ID: "task_safe", Goal: "fix $HOME `echo nope` and 'quotes'", Mode: domain.ModeChange, Risk: "medium",
		Status: domain.PhaseNew, Workspace: domain.Workspace{Root: "/tmp/repo with spaces"},
	}
	actions := structuredNextForCase(c)
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	want := "cortex -C '/tmp/repo with spaces' open 'fix $HOME `echo nope` and '\"'\"'quotes'\"'\"'' --mode change --risk medium"
	if actions[0].Command != want {
		t.Fatalf("command = %q, want %q", actions[0].Command, want)
	}
	if actions[0].Arguments["workspace"] != c.Workspace.Root {
		t.Fatalf("structured workspace = %#v", actions[0].Arguments["workspace"])
	}
}

func TestInterruptedOpenCommandCarriesAllMatchingMetadata(t *testing.T) {
	c := &domain.CaseFile{
		ID: "task_review", Goal: "review callback", Mode: domain.ModeReview, Risk: "high",
		Status: domain.PhaseOrienting, Actor: "agent-a", ParentTaskID: "task_parent",
		Surfaces:  []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser},
		Workspace: domain.Workspace{Root: "/tmp/repo"},
	}
	action := structuredNextForCase(c)[0]
	for _, want := range []string{
		"-C /tmp/repo", "--mode review", "--risk high", "--surface code",
		"--surface browser", "--actor agent-a", "--parent task_parent",
	} {
		if !strings.Contains(action.Command, want) {
			t.Errorf("recovery command missing %q: %s", want, action.Command)
		}
	}
	if strings.Contains(action.Command, "--idempotency-key") {
		t.Fatalf("non-keyed case invented an idempotency key: %s", action.Command)
	}

	// The structured command metadata must select the same existing case; this
	// guards the command string and Tool+Arguments projections together.
	workspace := testRepo(t)
	k := newTestKernel(t, workspace)
	parent, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "parent"}})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: c.Goal, Mode: c.Mode, Risk: c.Risk, Surfaces: c.Surfaces,
		Actor: c.Actor, ParentTaskID: parent.TaskID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	retried, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: c.Goal, Mode: c.Mode, Risk: c.Risk, Surfaces: c.Surfaces,
		Actor: c.Actor, ParentTaskID: parent.TaskID,
	}})
	if err != nil || retried.TaskID != existing.TaskID {
		t.Fatalf("metadata-preserving retry duplicated case: first=%s retry=%+v err=%v", existing.TaskID, retried, err)
	}
}

func TestStructuredActionsRecoverInterruptedOrientation(t *testing.T) {
	workspace := testRepo(t)
	k := newTestKernel(t, workspace)
	c := &domain.CaseFile{
		SchemaVersion: domain.SchemaVersion, ID: "task_interrupted_open", CreatedAt: time.Now().UTC(),
		Goal: "finish orientation", Mode: domain.ModeChange, Status: domain.PhaseNew, Risk: "medium",
		Surfaces: []domain.Surface{domain.SurfaceCode}, Actor: "agent-a", IdempotencyKey: "open-recovery",
		Workspace: domain.Workspace{Root: workspace, Repository: "repo"},
	}
	if err := k.Store().Create(c); err != nil {
		t.Fatal(err)
	}

	report, err := k.Status(context.Background(), c.ID, "standard")
	if err != nil || len(report.Actions) != 1 {
		t.Fatalf("status: %+v (%v)", report, err)
	}
	action := report.Actions[0]
	if action.Tool != "cortex_open_task" || len(action.Inputs) != 0 || action.Arguments["taskId"] != nil {
		t.Fatalf("interrupted orientation action is not retry-safe: %+v", action)
	}
	if action.Arguments["workspace"] != workspace || action.Arguments["goal"] != c.Goal || action.Arguments["idempotencyKey"] != c.IdempotencyKey {
		t.Fatalf("interrupted orientation action lost durable inputs: %+v", action)
	}

	resumed, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: c.Goal, Mode: c.Mode, Risk: c.Risk, Surfaces: c.Surfaces,
		Actor: c.Actor, IdempotencyKey: c.IdempotencyKey,
	}})
	if err != nil || !resumed.OK || resumed.TaskID != c.ID || resumed.Phase != domain.PhaseInvestigating {
		t.Fatalf("projected open action could not recover the case: %+v (%v)", resumed, err)
	}
}

func TestStructuredDecisionActionsRecoverHalfCommittedStates(t *testing.T) {
	t.Run("pending record before pause", func(t *testing.T) {
		workspace := testRepo(t)
		k := newTestKernel(t, workspace)
		started, _ := k.StartTask(context.Background(), StartInput{Goal: "choose a repair"})
		decision, err := k.buildDecision(RequestDecisionInput{
			TaskID: started.TaskID, Question: "Which repair?", Requester: "agent-a",
			Options: decisionOptions("nothing-sensitive"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := k.Store().AppendDecision(started.TaskID, decision); err != nil {
			t.Fatal(err)
		}

		report, err := k.Status(context.Background(), started.TaskID, "standard")
		if err != nil || len(report.Actions) != 1 {
			t.Fatalf("status: %+v (%v)", report, err)
		}
		action := report.Actions[0]
		if action.Tool != "cortex_request_decision" || len(action.Inputs) != 0 {
			t.Fatalf("pending repair action = %+v", action)
		}
		for _, want := range []string{"cortex -C", "decision request", "--question", "--option", "--requester"} {
			if !strings.Contains(action.Command, want) {
				t.Fatalf("pending repair command missing %q: %+v", want, action)
			}
		}
		if action.Arguments["workspace"] != workspace || action.Arguments["question"] != decision.Question || action.Arguments["requester"] != decision.Requester {
			t.Fatalf("pending repair action lost durable request: %+v", action)
		}
		if options, ok := action.Arguments["options"].([]domain.DecisionOption); !ok || len(options) != len(decision.Options) {
			t.Fatalf("pending repair action options = %#v", action.Arguments["options"])
		}

		recovered, err := k.RequestDecision(RequestDecisionInput{TaskID: started.TaskID})
		if err != nil || !recovered.OK || recovered.Phase != domain.PhaseNeedsHumanDecision {
			t.Fatalf("projected request retry did not repair pause: %+v (%v)", recovered, err)
		}
	})

	t.Run("answered record before resume", func(t *testing.T) {
		workspace := testRepo(t)
		k := newTestKernel(t, workspace)
		started, _ := k.StartTask(context.Background(), StartInput{Goal: "choose a repair"})
		requested, _ := k.RequestDecision(RequestDecisionInput{
			TaskID: started.TaskID, Question: "Which repair?", Requester: "agent-a",
			Options: decisionOptions("nothing-sensitive"),
		})
		decisionID := requested.Artifacts[0].ID
		if _, err := k.Store().AnswerDecision(
			started.TaskID, decisionID, "small", "human", "ev_interrupted_answer", time.Now().UTC(), false,
		); err != nil {
			t.Fatal(err)
		}

		report, err := k.Status(context.Background(), started.TaskID, "standard")
		if err != nil || len(report.Actions) != 1 {
			t.Fatalf("status: %+v (%v)", report, err)
		}
		action := report.Actions[0]
		if action.Tool != "cortex_answer_decision" || action.Arguments["resume"] != true || action.Arguments["workspace"] != workspace || len(action.Inputs) != 0 {
			t.Fatalf("answered recovery action is not directly invokable: %+v", action)
		}

		resumed, err := k.ResumeDecision(started.TaskID)
		if err != nil || !resumed.OK || resumed.Phase != domain.PhaseInvestigating {
			t.Fatalf("projected resume did not recover case: %+v (%v)", resumed, err)
		}
	})
}

func TestOpenResumeHydratesExactPendingDecision(t *testing.T) {
	workspace := testRepo(t)
	k := newTestKernel(t, workspace)
	opened, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "pause and retry", IdempotencyKey: "paused-open", Actor: "agent-a",
	}})
	paused, _ := k.RequestDecision(RequestDecisionInput{
		TaskID: opened.TaskID, Question: "Which repair?", Requester: "agent-a",
		Options: decisionOptions("nothing-sensitive"),
	})
	decisionID := paused.Artifacts[0].ID

	retried, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "lost response text can differ", IdempotencyKey: "paused-open", Actor: "agent-a",
	}})
	if err != nil || !retried.OK || retried.TaskID != opened.TaskID || len(retried.Actions) != 1 {
		t.Fatalf("paused open retry: %+v (%v)", retried, err)
	}
	action := retried.Actions[0]
	if action.Tool != "cortex_answer_decision" || action.Arguments["decisionId"] != decisionID || action.Arguments["workspace"] != workspace || strings.Contains(action.Command, "<decisionId>") {
		t.Fatalf("paused open retry did not hydrate exact continuation: %+v", action)
	}
}

func TestEnvelopeActionsKeepVerifiedResumeOnRemember(t *testing.T) {
	workspace := testRepo(t)
	k := newTestKernel(t, workspace)
	c := &domain.CaseFile{
		ID: "task_verified_resume", Goal: "resume verified work", Mode: domain.ModeChange,
		Status:               domain.PhaseVerifying,
		Workspace:            domain.Workspace{Root: workspace},
		IdempotencyKey:       "verified-resume-key",
		VerificationRequired: []string{"codemap_review"},
	}
	if err := k.Store().Create(c); err != nil {
		t.Fatal(err)
	}
	if err := k.Store().AppendVerification(c.ID, domain.VerificationRecord{
		ID: "vr_verified_resume", Claim: "structural review", Surface: domain.SurfaceCode,
		Purpose: domain.VerificationPurposeVerifierRun, Requirement: "codemap_review",
		Tool: "codemap", Status: domain.VerifyPassed, Binding: domain.VerificationBound,
	}); err != nil {
		t.Fatal(err)
	}

	env := k.envelope(c, "resumed", nil, nil, nil)
	if len(env.Actions) != 1 || env.Actions[0].Tool != "cortex_remember" {
		t.Fatalf("verified resume actions = %+v, want remember without a redundant verify", env.Actions)
	}

	// OpenTask uses the same action projection when it resumes an existing case.
	opened, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: c.Goal, Mode: domain.ModeChange, IdempotencyKey: c.IdempotencyKey,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if opened.TaskID != c.ID || len(opened.Actions) != 1 || opened.Actions[0].Tool != "cortex_remember" {
		t.Fatalf("open actions = %+v, want remember", opened.Actions)
	}
}
