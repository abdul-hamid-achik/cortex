package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/contracttest"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const conformanceSizeBehavior = "model-visible JSON is bounded by the kernel projection; raw tool output is excluded"

func assertPublicFixture(t *testing.T, id, generatedBy, classification, sizeBehavior string, payload any, workspace string) {
	t.Helper()
	replacements := map[string]string{}
	if workspace != "" {
		replacements[workspace] = "$WORKSPACE"
		repository := filepath.Base(workspace)
		replacements[`"repository":"`+repository+`"`] = `"repository":"$REPOSITORY"`
		replacements["workspace "+repository] = "workspace $REPOSITORY"
		replacements["repo:"+repository] = "repo:$REPOSITORY"
	}
	fixture, err := contracttest.NewFixture(id, generatedBy, classification, sizeBehavior, payload, replacements)
	if err != nil {
		t.Fatalf("build fixture %s: %v", id, err)
	}
	if err := contracttest.AssertGolden(id, fixture); err != nil {
		t.Fatal(err)
	}
}

func conformanceAdapters() []adapters.Adapter {
	return []adapters.Adapter{
		&fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover}, result: adapters.Result{
			Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "semantic_search", Claim: "callback.go is a candidate", Confidence: "medium",
				Location: &adapters.Location{File: "src/callback.go", StartLine: 1, EndLine: 2}}},
		}},
		&fakeAdapter{name: "veclite", caps: []adapters.Capability{adapters.CapabilityRecall}, result: adapters.Result{
			Status: adapters.StatusAuthoritative,
			Facts:  []adapters.Fact{{Kind: "model_inference", Claim: "no prior disproof matched", Confidence: "low"}},
		}},
		&fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure}, result: adapters.Result{
			Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "callback diff is structurally sound", Confidence: "high",
				Location: &adapters.Location{File: "src/callback.go", StartLine: 1, EndLine: 2}}},
		}},
	}
}

func plannedConformanceTask(t *testing.T, actor string, extras ...adapters.Adapter) (*Kernel, string, string) {
	t.Helper()
	workspace := testRepo(t)
	all := conformanceAdapters()
	all = append(all, extras...)
	k := newTestKernel(t, workspace, all...)
	opened, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "repair callback contract", Actor: actor, IdempotencyKey: "contract-lifecycle-v1",
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode},
	}})
	if err != nil || !opened.OK {
		t.Fatalf("open conformance task: %+v (%v)", opened, err)
	}
	planned, err := k.Plan(PlanInput{
		TaskID: opened.TaskID, Uncertainty: "runtime coverage remains uncertain",
		Hypotheses: []HypothesisInput{{
			Statement:  "the callback implementation violates the contract",
			DisproveBy: "review the exact callback diff",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"codemap_review"},
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan conformance task: %+v (%v)", planned, err)
	}
	return k, workspace, opened.TaskID
}

func TestPublicConformanceLifecycleSuccesses(t *testing.T) {
	workspace := testRepo(t)
	k := newTestKernel(t, workspace, conformanceAdapters()...)
	opened, err := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
		Goal: "repair callback contract", Actor: "agent-a", IdempotencyKey: "contract-lifecycle-v1",
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode},
	}})
	if err != nil || !opened.OK {
		t.Fatalf("open: %+v (%v)", opened, err)
	}
	assertPublicFixture(t, "open_task", "TestPublicConformanceLifecycleSuccesses/open_task", "canonical", conformanceSizeBehavior, opened, workspace)

	investigated, err := k.Investigate(context.Background(), InvestigateInput{
		TaskID: opened.TaskID, Question: "HandleCallback", Depth: "standard",
	})
	if err != nil || !investigated.OK {
		t.Fatalf("investigate: %+v (%v)", investigated, err)
	}
	assertPublicFixture(t, "investigate", "TestPublicConformanceLifecycleSuccesses/investigate", "canonical", conformanceSizeBehavior, investigated, workspace)

	planned, err := k.Plan(PlanInput{
		TaskID: opened.TaskID, Uncertainty: "runtime coverage remains uncertain",
		Hypotheses: []HypothesisInput{{
			Statement:  "the callback implementation violates the contract",
			DisproveBy: "review the exact callback diff",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"codemap_review"},
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan: %+v (%v)", planned, err)
	}
	assertPublicFixture(t, "plan", "TestPublicConformanceLifecycleSuccesses/plan", "canonical", conformanceSizeBehavior, planned, workspace)

	// Contract generation performs several real Git and store operations. Use
	// the maximum supported test lease so race instrumentation or a loaded CI
	// host cannot make the public success trajectory time-dependent.
	begun, err := k.BeginChange(BeginChangeInput{TaskID: opened.TaskID, Actor: "agent-a", TTL: MaxChangeLeaseTTL})
	if err != nil || !begun.OK {
		t.Fatalf("begin change: %+v (%v)", begun, err)
	}
	assertPublicFixture(t, "begin_change", "TestPublicConformanceLifecycleSuccesses/begin_change", "canonical", conformanceSizeBehavior, begun, workspace)

	if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, err := k.Verify(context.Background(), VerifyInput{
		TaskID: opened.TaskID, Actor: "agent-a", Claims: []string{"callback diff is structurally sound"},
	})
	if err != nil || !verified.OK {
		t.Fatalf("verify: %+v (%v)", verified, err)
	}
	assertPublicFixture(t, "verify", "TestPublicConformanceLifecycleSuccesses/verify", "canonical", conformanceSizeBehavior, verified, workspace)

	status, err := k.Status(context.Background(), opened.TaskID, "standard")
	if err != nil || !status.OK {
		t.Fatalf("status: %+v (%v)", status, err)
	}
	assertPublicFixture(t, "status", "TestPublicConformanceLifecycleSuccesses/status", "canonical", conformanceSizeBehavior, status, workspace)

	remembered, err := k.Remember(context.Background(), RememberInput{
		TaskID: opened.TaskID, Outcome: "callback contract repaired",
	})
	if err != nil || !remembered.OK {
		t.Fatalf("remember: %+v (%v)", remembered, err)
	}
	assertPublicFixture(t, "remember", "TestPublicConformanceLifecycleSuccesses/remember", "canonical", conformanceSizeBehavior, remembered, workspace)

	handoff, err := BuildHandoffIn(workspace, opened.TaskID, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	assertPublicFixture(t, "handoff", "TestPublicConformanceLifecycleSuccesses/handoff", "canonical", "handoff JSON is hard-capped; complete proof closure is retained atomically or omitted with a warning", handoff, workspace)
}

func TestPublicConformanceStructuralRejections(t *testing.T) {
	t.Run("plan missing disproof", func(t *testing.T) {
		k := newTestKernel(t, testRepo(t))
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "repair callback"})
		got, _ := k.Plan(PlanInput{
			TaskID: opened.TaskID, Uncertainty: "unknown",
			Hypotheses:     []HypothesisInput{{Statement: "callback is wrong"}},
			ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		})
		assertPublicFixture(t, "plan_missing_disproof", "TestPublicConformanceStructuralRejections/plan_missing_disproof", "canonical", conformanceSizeBehavior, got, k.cfg.Workspace)
	})

	t.Run("plan missing boundary", func(t *testing.T) {
		k := newTestKernel(t, testRepo(t))
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "repair callback", Mode: domain.ModeChange})
		got, _ := k.Plan(PlanInput{
			TaskID: opened.TaskID, Uncertainty: "unknown",
			Hypotheses: []HypothesisInput{{Statement: "callback is wrong", DisproveBy: "inspect the diff"}},
		})
		assertPublicFixture(t, "plan_missing_boundary", "TestPublicConformanceStructuralRejections/plan_missing_boundary", "canonical", conformanceSizeBehavior, got, k.cfg.Workspace)
	})

	t.Run("lease conflict and wrong actor", func(t *testing.T) {
		k, workspace, taskID := plannedConformanceTask(t, "agent-a")
		if begun, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a", TTL: time.Minute}); !begun.OK {
			t.Fatalf("begin: %+v", begun)
		}
		conflict, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-b", TTL: time.Minute})
		assertPublicFixture(t, "begin_change_lease_conflict", "TestPublicConformanceStructuralRejections/begin_change_lease_conflict", "canonical", conformanceSizeBehavior, conflict, workspace)
		if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 2 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wrong, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, Actor: "agent-b"})
		assertPublicFixture(t, "verify_wrong_actor", "TestPublicConformanceStructuralRejections/verify_wrong_actor", "canonical", conformanceSizeBehavior, wrong, workspace)
	})

	t.Run("no diff without acknowledgement", func(t *testing.T) {
		k, workspace, taskID := plannedConformanceTask(t, "agent-a")
		if begun, _ := k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a"}); !begun.OK {
			t.Fatalf("begin: %+v", begun)
		}
		got, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, Actor: "agent-a"})
		assertPublicFixture(t, "verify_no_diff_without_ack", "TestPublicConformanceStructuralRejections/verify_no_diff_without_ack", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("missing exact acceptance contract", func(t *testing.T) {
		workspace := testRepo(t)
		k := newTestKernel(t, workspace, conformanceAdapters()...)
		criterion := domain.AcceptanceCriterion{ID: "callback_contract", Statement: "callback contract passes"}
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "repair callback", Risk: "low", AcceptanceCriteria: []domain.AcceptanceCriterion{criterion}})
		_, _ = k.Plan(PlanInput{
			TaskID: opened.TaskID, Uncertainty: "unknown",
			Hypotheses:     []HypothesisInput{{Statement: "callback is wrong", DisproveBy: "review the diff"}},
			ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
			Verification:   []string{"codemap_review"},
		})
		if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 3 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _ := k.Verify(context.Background(), VerifyInput{TaskID: opened.TaskID, ClaimSpecs: []domain.VerificationClaim{{
			ID: criterion.ID, Statement: "a different callback statement", Surface: domain.SurfaceCode, Contract: "codemap_review",
		}}})
		assertPublicFixture(t, "verify_missing_exact_contract", "TestPublicConformanceStructuralRejections/verify_missing_exact_contract", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("remember unverified without acknowledgement", func(t *testing.T) {
		workspace := testRepo(t)
		codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure}, result: adapters.Result{Status: adapters.StatusUnavailable}}
		k := newTestKernel(t, workspace, codemap)
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "repair callback", Risk: "low"})
		_, _ = k.Plan(PlanInput{
			TaskID: opened.TaskID, Uncertainty: "unknown",
			Hypotheses:     []HypothesisInput{{Statement: "callback is wrong", DisproveBy: "review the diff"}},
			ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Verification: []string{"codemap_review"},
		})
		if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 4 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: opened.TaskID}); !verified.OK {
			t.Fatalf("verify should retain honest unavailable receipt: %+v", verified)
		}
		got, _ := k.Remember(context.Background(), RememberInput{TaskID: opened.TaskID, Outcome: "attempted repair"})
		assertPublicFixture(t, "remember_unverified_without_ack", "TestPublicConformanceStructuralRejections/remember_unverified_without_ack", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("immutable acceptance mismatch", func(t *testing.T) {
		workspace := testRepo(t)
		k := newTestKernel(t, workspace)
		first := []domain.AcceptanceCriterion{{ID: "callback_contract", Statement: "callback contract passes"}}
		opened, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
			Goal: "repair callback", IdempotencyKey: "immutable-contract", AcceptanceCriteria: first,
		}})
		if !opened.OK {
			t.Fatalf("open: %+v", opened)
		}
		got, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{
			Goal: "repair callback", IdempotencyKey: "immutable-contract",
			AcceptanceCriteria: []domain.AcceptanceCriterion{{ID: "callback_contract", Statement: "different statement"}},
		}})
		assertPublicFixture(t, "immutable_acceptance_mismatch", "TestPublicConformanceStructuralRejections/immutable_acceptance_mismatch", "canonical", conformanceSizeBehavior, got, workspace)
	})
}

func TestPublicConformanceDegradedAndEdgeCases(t *testing.T) {
	t.Run("tool unavailable", func(t *testing.T) {
		workspace := testRepo(t)
		unavailable := func(name string, capability adapters.Capability) adapters.Adapter {
			return &fakeAdapter{name: name, caps: []adapters.Capability{capability}, result: adapters.Result{
				Status: adapters.StatusUnavailable, Summary: name + " unavailable in fixture",
				Warnings: []string{name + " unavailable in fixture"},
				Facts: []adapters.Fact{{
					Kind: "tool_unavailable", Claim: name + " is unavailable; dependent results are blocked", Confidence: "unknown",
				}},
			}}
		}
		k := newTestKernel(t, workspace,
			unavailable("vecgrep", adapters.CapabilityDiscover),
			unavailable("veclite", adapters.CapabilityRecall),
			unavailable("codemap", adapters.CapabilityStructure),
		)
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "locate callback"})
		got, _ := k.Investigate(context.Background(), InvestigateInput{TaskID: opened.TaskID, Question: "HandleCallback"})
		assertPublicFixture(t, "tool_unavailable", "TestPublicConformanceDegradedAndEdgeCases/tool_unavailable", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("blocked command verifier", func(t *testing.T) {
		t.Setenv("CORTEX_APPROVE_COMMANDS", "")
		workspace := testRepo(t)
		command := &fakeAdapter{name: "command", result: adapters.Result{Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictPassed}}
		k := newTestKernel(t, workspace, command)
		k.cfg.Verifiers = map[string]config.CommandVerifier{
			"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
		}
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "verify safely", Risk: "low"})
		_, _ = k.Plan(PlanInput{
			TaskID: opened.TaskID, Uncertainty: "repository commands are untrusted",
			Hypotheses:     []HypothesisInput{{Statement: "tests should pass", DisproveBy: "the unit verifier fails"}},
			ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Verification: []string{"command:unit"},
		})
		if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 5 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _ := k.Verify(context.Background(), VerifyInput{TaskID: opened.TaskID})
		handoff, err := BuildHandoffIn(workspace, opened.TaskID, time.Unix(10, 0))
		if err != nil {
			t.Fatal(err)
		}
		assertPublicFixture(t, "blocked_command_verifier", "TestPublicConformanceDegradedAndEdgeCases/blocked_command_verifier", "canonical", conformanceSizeBehavior, map[string]any{
			"verify": got, "handoff": handoff,
		}, workspace)
	})

	t.Run("stale receipt", func(t *testing.T) {
		k, workspace, taskID := plannedConformanceTask(t, "agent-a")
		_, _ = k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a"})
		if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 6 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, Actor: "agent-a"}); !verified.OK {
			t.Fatalf("verify: %+v", verified)
		}
		if err := os.WriteFile(filepath.Join(workspace, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 7 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _ := k.Status(context.Background(), taskID, "standard")
		assertPublicFixture(t, "stale_receipt", "TestPublicConformanceDegradedAndEdgeCases/stale_receipt", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("scope unknown", func(t *testing.T) {
		k, workspace, taskID := plannedConformanceTask(t, "agent-a")
		_, _ = k.BeginChange(BeginChangeInput{TaskID: taskID, Actor: "agent-a"})
		caseFile, err := k.Store().Load(taskID)
		if err != nil {
			t.Fatal(err)
		}
		caseFile.Workspace.BaseRef = "missing-review-base"
		if err := k.Store().Save(caseFile); err != nil {
			t.Fatal(err)
		}
		got, _ := k.Status(context.Background(), taskID, "standard")
		assertPublicFixture(t, "scope_unknown", "TestPublicConformanceDegradedAndEdgeCases/scope_unknown", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("pending decision", func(t *testing.T) {
		workspace := testRepo(t)
		k := newTestKernel(t, workspace)
		opened, _ := k.StartTask(context.Background(), StartInput{Goal: "choose repair"})
		requested, _ := k.RequestDecision(RequestDecisionInput{
			TaskID: opened.TaskID, Question: "Which repair should proceed?", Requester: "agent-a",
			Options: []domain.DecisionOption{
				{ID: "small", Label: "Small repair", Consequence: "Touches one file"},
				{ID: "broad", Label: "Broad repair", Consequence: "Touches several files"},
			},
		})
		if !requested.OK {
			t.Fatalf("request decision: %+v", requested)
		}
		got, _ := k.Status(context.Background(), opened.TaskID, "standard")
		assertPublicFixture(t, "pending_decision", "TestPublicConformanceDegradedAndEdgeCases/pending_decision", "canonical", conformanceSizeBehavior, got, workspace)
	})

	t.Run("bounded handoff", func(t *testing.T) {
		huge := strings.Repeat("界", maxHandoffBytes/3)
		handoff := Handoff{
			SchemaVersion: 1, GeneratedAt: time.Unix(10, 0).UTC(), TaskID: "task_bounded", Revision: 9,
			Goal: huge, Phase: domain.PhaseNeedsHumanDecision, Mode: domain.ModeChange, Risk: "medium",
			Workspace:    domain.Workspace{Root: "/synthetic/repository", Repository: "repository"},
			Verification: VerificationAssessment{Outcome: VerificationPartial, MissingRequired: []string{huge}},
			Decisions: []domain.Decision{{
				ID: "decision_pending", Question: huge, Requester: "agent-a", Status: domain.DecisionPending,
				Options: []domain.DecisionOption{{ID: "continue", Label: huge, Consequence: huge}},
			}},
			Actions: []domain.NextAction{{Tool: "cortex_answer_decision", Arguments: map[string]any{
				"taskId": "task_bounded", "decisionId": "decision_pending", "payload": huge,
			}}},
		}
		for i := 0; i < maxHandoffEvidence+5; i++ {
			handoff.Evidence = append(handoff.Evidence, domain.FactView{
				ID: fmt.Sprintf("evidence_%02d", i), Claim: huge, Source: "synthetic", Confidence: domain.ConfidenceLow,
			})
		}
		got := boundHandoff(handoff)
		assertPublicFixture(t, "bounded_handoff", "TestPublicConformanceDegradedAndEdgeCases/bounded_handoff", "illustrative", "handoff JSON is capped at 128 KiB while retaining identity and a continuation", got, "")
	})

	t.Run("proof closure overflow", func(t *testing.T) {
		ledger := completionProofLedger(2, 32, func(batch, claim int) string {
			return fmt.Sprintf("criterion-%d-%d-%s", batch, claim, strings.Repeat("界", 1_350))
		})
		projection := projectHandoffReceipts(domain.PhaseComplete, VerificationVerified, ledger)
		got := boundHandoff(Handoff{
			SchemaVersion: 1, TaskID: "task_oversize_proof", Revision: 19,
			Phase: domain.PhaseComplete, Mode: domain.ModeChange,
			Workspace:    domain.Workspace{Repository: "synthetic"},
			Verification: VerificationAssessment{Outcome: VerificationVerified}, Receipts: projection.receipts,
		})
		assertPublicFixture(t, "proof_closure_overflow", "TestPublicConformanceDegradedAndEdgeCases/proof_closure_overflow", "illustrative", "an oversize complete proof closure is omitted atomically with an explicit warning", got, "")
	})

	t.Run("unsupported future schema", func(t *testing.T) {
		future := map[string]any{"contractVersion": contracttest.Version + 1, "id": "future"}
		encoded, err := json.Marshal(future)
		if err != nil {
			t.Fatal(err)
		}
		_, decodeErr := contracttest.Decode(encoded)
		if decodeErr == nil {
			t.Fatal("future contract fixture was accepted")
		}
		payload := map[string]any{"input": future, "error": decodeErr.Error()}
		assertPublicFixture(t, "unsupported_future_schema", "TestPublicConformanceDegradedAndEdgeCases/unsupported_future_schema", "canonical", "unknown future fixture schemas are rejected before payload consumption", payload, "")
	})
}
