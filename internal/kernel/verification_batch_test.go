package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

type workspaceMutatingVerifier struct {
	path string
}

type workspaceMutatingBehaviorVerifier struct {
	path string
}

type blockingVerifier struct {
	entered chan struct{}
	release chan struct{}
}

func (v *blockingVerifier) Name() string { return "codemap" }

func (v *blockingVerifier) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapabilityStructure}
}

func (v *blockingVerifier) Health(context.Context) error { return nil }

func (v *blockingVerifier) Execute(ctx context.Context, _ adapters.Request) (adapters.Result, error) {
	close(v.entered)
	select {
	case <-ctx.Done():
		return adapters.Result{}, ctx.Err()
	case <-v.release:
		return adapters.Result{
			Tool: "codemap", Status: adapters.StatusAuthoritative, Summary: "reviewed", Raw: "private verifier raw",
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "discardable verifier fact", Confidence: "high"}},
		}, nil
	}
}

func (v *workspaceMutatingVerifier) Name() string { return "codemap" }

func (v *workspaceMutatingVerifier) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapabilityStructure}
}

func (v *workspaceMutatingVerifier) Health(context.Context) error { return nil }

func (v *workspaceMutatingVerifier) Execute(context.Context, adapters.Request) (adapters.Result, error) {
	if err := os.WriteFile(v.path, []byte("package src\nfunc HandleCallback(){ _ = 2 }\n"), 0o644); err != nil {
		return adapters.Result{}, err
	}
	return adapters.Result{
		Tool: "codemap", Status: adapters.StatusAuthoritative, Summary: "reviewed",
		Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}},
	}, nil
}

func (v *workspaceMutatingBehaviorVerifier) Name() string { return "cairntrace" }

func (v *workspaceMutatingBehaviorVerifier) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapabilityBrowser}
}

func (v *workspaceMutatingBehaviorVerifier) Health(context.Context) error { return nil }

func (v *workspaceMutatingBehaviorVerifier) Execute(context.Context, adapters.Request) (adapters.Result, error) {
	if err := os.WriteFile(v.path, []byte("package src\nfunc HandleCallback(){ _ = 3 }\n"), 0o644); err != nil {
		return adapters.Result{}, err
	}
	return adapters.Result{
		Tool: "cairntrace", Status: adapters.StatusAuthoritative, Summary: "browser flow passed",
		Facts: []adapters.Fact{{Kind: "browser_run", Claim: "flow passed", Confidence: "high"}},
	}, nil
}

func TestVerifyDowngradesBatchWhenWorkspaceChangesDuringRun(t *testing.T) {
	ws := testRepo(t)
	path := filepath.Join(ws, "src", "callback.go")
	k := newTestKernel(t, ws, &workspaceMutatingVerifier{path: path})
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "bind verifier proof to one diff", Mode: domain.ModeChange,
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode},
	})
	planned, _ := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "the callback needs a change", DisproveBy: "review the callback diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}, Symbols: []string{"HandleCallback"}},
		Uncertainty:    "the verifier may race another editor",
	})
	if !planned.OK {
		t.Fatalf("plan: %+v", planned)
	}
	if err := os.WriteFile(path, []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	verified, _ := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, Claims: []string{"the diff is structurally sound"},
	})
	if !verified.OK || !hasWarning(verified.Warnings, "downgraded to inconclusive") {
		t.Fatalf("verify should surface an unbound batch: %+v", verified)
	}
	receipts, err := k.Store().Verifications(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) < 2 || receipts[0].BatchID == "" {
		t.Fatalf("expected one identified verifier batch, got %+v", receipts)
	}
	for _, receipt := range receipts {
		if receipt.BatchID != receipts[0].BatchID || receipt.Binding != domain.VerificationUnbound || receipt.Proven() {
			t.Fatalf("receipt escaped unbound batch downgrade: %+v", receipt)
		}
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if status.VerificationOutcome == VerificationVerified || len(status.MissingVerification) == 0 {
		t.Fatalf("unbound latest batch must mask older proof and stay non-proving: %+v", status)
	}
}

func TestUnboundBehavioralRunDoesNotAnnotateCodemap(t *testing.T) {
	ws := testRepo(t)
	path := filepath.Join(ws, "src", "callback.go")
	codemap := &fakeAdapter{
		name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Tool: "codemap", Status: adapters.StatusAuthoritative, Summary: "reviewed"},
	}
	k := newTestKernel(t, ws, codemap, &workspaceMutatingBehaviorVerifier{path: path})
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "bind behavior before annotation", Mode: domain.ModeChange, Risk: "low",
		Surfaces: []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser},
	})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback needs repair", DisproveBy: "run browser flow"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}, Symbols: []string{"HandleCallback"}},
		Verification:   []string{"codemap_review", "cairntrace_flow"}, Uncertainty: "another editor may race",
	})
	if err := os.WriteFile(path, []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, err := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, BrowserSpec: "specs/callback.yml",
		ClaimSpecs: []domain.VerificationClaim{{
			ID: "browser_callback", Statement: "callback works", Surface: domain.SurfaceBrowser,
			Verifier: "cairntrace", Contract: "specs/callback.yml", Required: true,
		}},
	})
	if err != nil || !verified.OK || !hasWarning(verified.Warnings, "annotations skipped") {
		t.Fatalf("unbound verify = %+v (%v)", verified, err)
	}
	for _, request := range codemap.requests() {
		if request.Operation == "annotate" {
			t.Fatalf("unbound behavioral result escaped into codemap: %+v", request)
		}
	}
}

func TestRememberFailsClosedWhenVerificationFreshnessCannotBeChecked(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{
		name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "reviewed"},
	}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "do not reuse unverifiable proof", Mode: domain.ModeChange,
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode},
	})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "the callback needs a change", DisproveBy: "review the callback diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "git metadata could become unavailable",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, Claims: []string{"the diff is structurally sound"},
	})
	if !verified.OK {
		t.Fatalf("verify: %+v", verified)
	}
	if err := os.Rename(filepath.Join(ws, ".git"), filepath.Join(ws, ".git-unavailable")); err != nil {
		t.Fatal(err)
	}

	remembered, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "done"})
	if remembered.OK || (!strings.Contains(remembered.Error, "verification is partial") && !strings.Contains(remembered.Error, "no adequate verification")) {
		t.Fatalf("remember must not trust proof whose freshness cannot be checked: %+v", remembered)
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if status.VerificationOutcome == VerificationVerified || !hasWarning(status.Warnings, "could not check verification freshness") {
		t.Fatalf("status must fail closed and explain freshness failure: %+v", status)
	}
}

func TestVerifyDiscardsBatchWhenCaseChangesDuringRun(t *testing.T) {
	ws := testRepo(t)
	verifier := &blockingVerifier{entered: make(chan struct{}), release: make(chan struct{})}
	k := newTestKernel(t, ws, verifier)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "bind verifier proof to one plan", Mode: domain.ModeChange,
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode}, Actor: "agent-a",
	})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "the callback needs a change", DisproveBy: "review the callback diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "the case may change concurrently",
	})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if begun, err := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-a", TTL: time.Minute}); err != nil || !begun.OK {
		t.Fatalf("begin change: %+v (%v)", begun, err)
	}
	beforeEvidence, err := k.Store().Evidence(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan domain.Envelope, 1)
	go func() {
		env, _ := k.Verify(context.Background(), VerifyInput{
			TaskID: started.TaskID, Actor: "agent-a", Claims: []string{"the diff is structurally sound"},
		})
		result <- env
	}()
	<-verifier.entered
	if released, err := k.ReleaseChangeLease(ReleaseChangeLeaseInput{TaskID: started.TaskID, Actor: "agent-a"}); err != nil || !released.OK {
		t.Fatalf("release lease: %+v (%v)", released, err)
	}
	if begun, err := k.BeginChange(BeginChangeInput{TaskID: started.TaskID, Actor: "agent-b", TTL: time.Minute}); err != nil || !begun.OK {
		t.Fatalf("transfer lease: %+v (%v)", begun, err)
	}
	close(verifier.release)
	verified := <-result
	if verified.OK || (!strings.Contains(verified.Error, "case changed while verification ran") && !strings.Contains(verified.Error, "change ownership changed")) {
		t.Fatalf("concurrent case update should discard verifier results: %+v", verified)
	}
	receipts, err := k.Store().Verifications(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("discarded verifier batch leaked receipts: %+v", receipts)
	}
	afterEvidence, err := k.Store().Evidence(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEvidence) != len(beforeEvidence) {
		t.Fatalf("discarded verifier batch leaked evidence: before=%d after=%+v", len(beforeEvidence), afterEvidence)
	}
	taskDir, _ := k.Store().TaskDir(started.TaskID)
	if entries, readErr := os.ReadDir(filepath.Join(taskDir, "raw")); readErr == nil && len(entries) != 0 {
		t.Fatalf("discarded verifier batch leaked raw blobs: %v", entries)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	commands, err := k.Store().Commands(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	foundReview := false
	for _, command := range commands {
		if command.Tool == "codemap" && command.Operation == "review" {
			foundReview = true
			if command.Actor != "agent-a" {
				t.Fatalf("verifier command was attributed after lease transfer: %+v", command)
			}
		}
	}
	if !foundReview {
		t.Fatal("missing discarded verifier audit record")
	}
}

func TestMediumAndHighRiskPlansCannotOmitStructuralReview(t *testing.T) {
	for _, risk := range []string{"medium", "high"} {
		t.Run(risk, func(t *testing.T) {
			k := newTestKernel(t, testRepo(t))
			started, _ := k.StartTask(context.Background(), StartInput{
				Goal: "keep risk policy structural", Mode: domain.ModeChange,
				Risk: risk, Surfaces: []domain.Surface{domain.SurfaceBrowser},
			})
			planned, _ := k.Plan(PlanInput{
				TaskID:         started.TaskID,
				Hypotheses:     []HypothesisInput{{Statement: "the callback needs a change", DisproveBy: "the browser flow passes unchanged"}},
				ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
				Verification:   []string{"cairntrace_flow"},
				Uncertainty:    "structural blast radius remains",
			})
			if !planned.OK {
				t.Fatalf("plan: %+v", planned)
			}
			caseFile, err := k.Store().Load(started.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if !containsString(caseFile.VerificationRequired, "codemap_review") {
				t.Fatalf("%s-risk plan omitted codemap_review: %v", risk, caseFile.VerificationRequired)
			}
		})
	}
}

func TestShowAndHandoffDoNotPresentStaleProofAsCurrent(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{
		name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "reviewed"},
	}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "keep handoffs current", Mode: domain.ModeChange,
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode},
	})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "the callback needs a change", DisproveBy: "review the callback diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "the workspace may move after verification",
	})
	path := filepath.Join(ws, "src", "callback.go")
	if err := os.WriteFile(path, []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, Claims: []string{"the diff is structurally sound"},
	})
	if !verified.OK {
		t.Fatalf("verify: %+v", verified)
	}
	if err := os.WriteFile(path, []byte("package src\nfunc HandleCallback(){ _ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ShowSession(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if view.VerificationAssessment.Outcome == VerificationVerified || len(view.StaleVerification) == 0 {
		t.Fatalf("show presented stale proof as current: %+v", view.VerificationAssessment)
	}
	for _, staleID := range view.StaleVerification {
		if !strings.HasPrefix(staleID, "vr_") {
			t.Fatalf("stale verification should identify receipts, got %q", staleID)
		}
	}
	handoff, err := BuildHandoff(started.TaskID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Verification.Outcome == VerificationVerified || len(handoff.Receipts) != 0 || !hasWarning(handoff.Warnings, "stale") {
		t.Fatalf("handoff presented stale proof as current: %+v", handoff)
	}
}

func TestCompletedSessionKeepsHistoricalVerificationAfterLaterWorkspaceChanges(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{
		name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "reviewed"},
	}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "preserve historical proof", Mode: domain.ModeChange,
		Risk: "low", Surfaces: []domain.Surface{domain.SurfaceCode},
	})
	_, _ = k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "the callback needs a change", DisproveBy: "review the callback diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "future tasks may edit the same file",
	})
	path := filepath.Join(ws, "src", "callback.go")
	if err := os.WriteFile(path, []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, Claims: []string{"the diff is structurally sound"},
	})
	remembered, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "callback updated"})
	if !remembered.OK {
		t.Fatalf("remember: %+v", remembered)
	}
	if err := os.WriteFile(path, []byte("package src\nfunc HandleCallback(){ _ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := ShowSession(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if view.VerificationAssessment.Outcome != VerificationVerified || len(view.StaleVerification) != 0 {
		t.Fatalf("later work retroactively invalidated a completed historical case: %+v", view)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
