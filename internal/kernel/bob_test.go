package kernel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// bobKernelAdapter returns request-aware Bob fixtures. In particular, path
// responses echo the requested path so strict kernel validation is exercised
// instead of being bypassed by a shared empty-path fixture.
type bobKernelAdapter struct {
	mu            sync.Mutex
	contextResult adapters.Result
	pathResult    func(adapters.Request) adapters.Result
	execute       func(context.Context, adapters.Request) (adapters.Result, error)
	reqs          []adapters.Request
}

func (b *bobKernelAdapter) Name() string { return "bob" }

func (b *bobKernelAdapter) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapabilityRepositoryContract}
}

func (b *bobKernelAdapter) Health(context.Context) error { return nil }

func (b *bobKernelAdapter) Execute(ctx context.Context, req adapters.Request) (adapters.Result, error) {
	b.mu.Lock()
	b.reqs = append(b.reqs, req)
	contextResult := b.contextResult
	pathResult := b.pathResult
	execute := b.execute
	b.mu.Unlock()
	if execute != nil {
		return execute(ctx, req)
	}

	var result adapters.Result
	switch req.Operation {
	case "context":
		result = contextResult
	case "path":
		if pathResult == nil {
			return adapters.Result{}, fmt.Errorf("unexpected bob path request")
		}
		result = pathResult(req)
	default:
		return adapters.Result{}, fmt.Errorf("unexpected bob operation %q", req.Operation)
	}
	result.Tool = "bob"
	return result, nil
}

func (b *bobKernelAdapter) setContextResult(result adapters.Result) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.contextResult = result
}

func (b *bobKernelAdapter) setPathResult(result func(adapters.Request) adapters.Result) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pathResult = result
}

func (b *bobKernelAdapter) setExecute(execute func(context.Context, adapters.Request) (adapters.Result, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.execute = execute
}

func (b *bobKernelAdapter) requests() []adapters.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]adapters.Request(nil), b.reqs...)
}

func writeBobManifest(t *testing.T, workspace string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, "bob.yaml"), []byte("schema_version: 1\nrecipe: go-agent-tool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bobContextFixtureResult(status adapters.Status) adapters.Result {
	return adapters.Result{
		Status: status, Summary: "Bob repository contract is clean", Raw: `{"schema_version":1,"ok":true}`,
		Facts: []adapters.Fact{{
			Kind: "repository_contract", Claim: "repository recipe is go-agent-tool@4; Bob repository state is clean", Confidence: "high",
			URI: "bob://context/v1/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Attributes: map[string]string{
				"schema_version": "1", "profile": "compact", "context_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"contract_digest":  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"repository_state": "clean", "recipe_id": "go-agent-tool", "recipe_version": "4",
				"plan_digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "plan_digest_version": "1",
			},
		}},
	}
}

func bobPathFixtureResult(path, effect string, playbooks []string) adapters.Result {
	playbookJSON := "[]"
	if len(playbooks) > 0 {
		quoted := make([]string, 0, len(playbooks))
		for _, value := range playbooks {
			quoted = append(quoted, fmt.Sprintf("%q", value))
		}
		playbookJSON = "[" + strings.Join(quoted, ",") + "]"
	}
	attributes := map[string]string{
		"schema_version": "1", "path": path, "classification": "managed", "state": "managed_in_sync",
		"human_edit_effect": effect, "recipe_id": "go-agent-tool", "recipe_version": "4",
		"extension_points": "[]", "related_playbooks": playbookJSON, "bob_truncated": "false",
	}
	if effect == "outside_bob_ownership" {
		attributes["classification"] = "extension_point"
		attributes["state"] = "extension_point"
		attributes["extension_points"] = `["cli.command_files"]`
	}
	return adapters.Result{
		Status: adapters.StatusAuthoritative, Summary: "Bob classified path", Raw: `{"schema_version":1,"ok":true}`,
		Facts: []adapters.Fact{{
			Kind: "repository_contract", Claim: fmt.Sprintf("path %s has Bob edit effect %s", path, effect), Confidence: "high",
			Location: &adapters.Location{File: path}, URI: "bob://path/v1/capture", Attributes: attributes,
		}},
	}
}

func TestBobRegistrationRequiresManifest(t *testing.T) {
	workspace := testRepo(t)
	t.Setenv("CORTEX_HOME", t.TempDir())
	without, err := New(config.For(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if without.Registry().Get("bob") != nil {
		t.Fatal("Bob must not be registered when bob.yaml is absent")
	}
	writeBobManifest(t, workspace)
	with, err := New(config.For(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if with.Registry().Get("bob") == nil {
		t.Fatal("Bob must be registered when bob.yaml is present")
	}
}

func TestBobOrientationIsStableNonVerifyingEvidence(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	secret := "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
	contextResult := bobContextFixtureResult(adapters.StatusAuthoritative)
	contextResult.Raw += "\nTOKEN=" + secret
	bob := &fakeAdapter{
		name: "bob", caps: []adapters.Capability{adapters.CapabilityRepositoryContract},
		byOp: map[string]adapters.Result{"context": contextResult},
	}
	k := newTestKernel(t, workspace, bob)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "respect repository ownership"})
	if err != nil || !started.OK || started.Phase != domain.PhaseInvestigating {
		t.Fatalf("start with Bob failed: env=%+v err=%v", started, err)
	}

	evidence, err := k.Store().Evidence(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	bobFacts := evidenceOfKind(evidence, domain.KindRepositoryContract)
	if len(bobFacts) != 1 {
		t.Fatalf("expected one repository-contract fact, got %d: %+v", len(bobFacts), evidence)
	}
	if bobFacts[0].Kind.CanVerify() {
		t.Fatal("Bob repository-contract evidence must never satisfy behavioral verification")
	}
	if !strings.Contains(bobFacts[0].RawRef, "/raw/raw_bob_context_") || !started.RawAvailable {
		t.Fatalf("Bob raw should be retained but only referenced, ref=%q rawAvailable=%v", bobFacts[0].RawRef, started.RawAvailable)
	}
	rawID := filepath.Base(bobFacts[0].RawRef)
	raw, err := k.Store().ReadRaw(started.TaskID, rawID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) || !strings.Contains(raw, "«redacted»") {
		t.Fatalf("stable Bob raw was not redacted: %q", raw)
	}

	caseFile, err := k.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = k.orientWithBob(context.Background(), caseFile)
	_, _ = k.orientWithBob(context.Background(), caseFile)
	evidence, _ = k.Store().Evidence(started.TaskID)
	if got := len(evidenceOfKind(evidence, domain.KindRepositoryContract)); got != 1 {
		t.Fatalf("orientation retries duplicated repository-contract evidence: %d", got)
	}
	rawEntries, err := os.ReadDir(filepath.Join(k.Store().Root(), started.TaskID, "raw"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rawEntries) != 1 {
		t.Fatalf("orientation retries duplicated raw captures: %d", len(rawEntries))
	}
}

func TestBobOpenRetryReprojectsStableOrientation(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	contextResult := bobContextFixtureResult(adapters.StatusAuthoritative)
	contextResult.Raw = `{"capture":"stable"}`
	bob := &bobKernelAdapter{contextResult: contextResult}
	k := newTestKernel(t, workspace, bob)
	input := OpenInput{StartInput: StartInput{Goal: "resume Bob orientation", IdempotencyKey: "bob-open-retry"}}

	first, err := k.OpenTask(context.Background(), input)
	if err != nil || !first.OK || !first.RawAvailable {
		t.Fatalf("first open = %+v err=%v", first, err)
	}
	retry, err := k.OpenTask(context.Background(), input)
	if err != nil || !retry.OK || !retry.RawAvailable {
		t.Fatalf("retry open = %+v err=%v", retry, err)
	}
	if got := len(factViewsOfKind(retry.Facts, domain.KindRepositoryContract)); got != 1 {
		t.Fatalf("retry did not reproject one Bob fact: %+v", retry.Facts)
	}
	if got := len(evidenceOfKind(mustEvidence(t, k, first.TaskID), domain.KindRepositoryContract)); got != 1 {
		t.Fatalf("public open retry duplicated Bob evidence: %d", got)
	}
	if got := bobRawFileCount(t, k, first.TaskID); got != 1 {
		t.Fatalf("public open retry duplicated Bob raw: %d", got)
	}
	contextCalls := 0
	for _, req := range bob.requests() {
		if req.Operation == "context" {
			contextCalls++
		}
	}
	if contextCalls != 2 {
		t.Fatalf("Bob context calls = %d, want initial orientation plus retry projection", contextCalls)
	}
}

func TestBobOpenRetryPreservesUnavailableDegradation(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	bob := &bobKernelAdapter{contextResult: adapters.Result{
		Status: adapters.StatusUnavailable, Summary: "bob unavailable",
		Warnings: []string{"bob unavailable"},
		Facts:    []adapters.Fact{{Kind: "tool_unavailable", Claim: "Bob unavailable", Confidence: "unknown"}},
	}}
	k := newTestKernel(t, workspace, bob)
	input := OpenInput{StartInput: StartInput{Goal: "resume missing Bob honestly", IdempotencyKey: "bob-open-degraded"}}
	first, err := k.OpenTask(context.Background(), input)
	if err != nil || !first.OK || !first.Degraded {
		t.Fatalf("first degraded open = %+v err=%v", first, err)
	}
	retry, err := k.OpenTask(context.Background(), input)
	if err != nil || !retry.OK || !retry.Degraded || !hasWarning(retry.Warnings, "bob unavailable") {
		t.Fatalf("retry hid Bob degradation: %+v err=%v", retry, err)
	}
	if findAction(retry.Actions, "bob_context") == nil {
		t.Fatalf("retry omitted Bob corrective action: %+v", retry.Actions)
	}
	if got := len(evidenceOfKind(mustEvidence(t, k, first.TaskID), domain.KindToolUnavailable)); got != 1 {
		t.Fatalf("retry duplicated unavailable evidence: %d", got)
	}
}

func TestBobOpenCancellationDoesNotPersistUnavailableEvidence(t *testing.T) {
	for _, reason := range []string{"canceled", "deadline"} {
		t.Run(reason, func(t *testing.T) {
			workspace := testRepo(t)
			writeBobManifest(t, workspace)
			bob := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
			k := newTestKernel(t, workspace, bob)
			input := OpenInput{StartInput: StartInput{
				Goal: "resume after canceled Bob orientation", IdempotencyKey: "bob-orientation-" + reason,
			}}

			var ctx context.Context
			var cancel context.CancelFunc
			switch reason {
			case "canceled":
				ctx, cancel = context.WithCancel(context.Background())
				bob.setExecute(func(callCtx context.Context, req adapters.Request) (adapters.Result, error) {
					if req.Operation == "context" {
						cancel()
						<-callCtx.Done()
					}
					return adapters.Result{
						Status: adapters.StatusUnavailable,
						Facts:  []adapters.Fact{{Kind: "tool_unavailable", Claim: "Bob unavailable", Confidence: "unknown"}},
					}, nil
				})
			case "deadline":
				ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
				bob.setExecute(func(callCtx context.Context, _ adapters.Request) (adapters.Result, error) {
					<-callCtx.Done()
					return adapters.Result{}, callCtx.Err()
				})
			}
			defer cancel()

			opened, err := k.OpenTask(ctx, input)
			wantErr := context.Canceled
			if reason == "deadline" {
				wantErr = context.DeadlineExceeded
			}
			if !errors.Is(err, wantErr) || opened.OK || opened.TaskID == "" {
				t.Fatalf("canceled open = %+v err=%v, want %v", opened, err, wantErr)
			}
			evidence := mustEvidence(t, k, opened.TaskID)
			if got := len(evidenceOfKind(evidence, domain.KindRepositoryContract)); got != 0 {
				t.Fatalf("canceled orientation persisted repository-contract evidence: %+v", evidence)
			}
			if got := len(evidenceOfKind(evidence, domain.KindToolUnavailable)); got != 0 {
				t.Fatalf("canceled orientation persisted false unavailable evidence: %+v", evidence)
			}
			caseFile, loadErr := k.Store().Load(opened.TaskID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if caseFile.Status == domain.PhaseInvestigating {
				t.Fatalf("canceled orientation committed investigating phase: %+v", caseFile)
			}

			bob.setExecute(nil)
			retry, retryErr := k.OpenTask(context.Background(), input)
			if retryErr != nil || !retry.OK || retry.Phase != domain.PhaseInvestigating {
				t.Fatalf("orientation retry = %+v err=%v", retry, retryErr)
			}
			evidence = mustEvidence(t, k, retry.TaskID)
			if got := len(evidenceOfKind(evidence, domain.KindRepositoryContract)); got != 1 {
				t.Fatalf("retry did not retain one Bob contract fact: %+v", evidence)
			}
			if got := len(evidenceOfKind(evidence, domain.KindToolUnavailable)); got != 0 {
				t.Fatalf("retry retained false unavailable evidence: %+v", evidence)
			}
		})
	}
}

func TestBobOrientationRetryDoesNotOverwriteStableRaw(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	first := bobContextFixtureResult(adapters.StatusAuthoritative)
	first.Raw = `{"capture":"first"}`
	bob := &bobKernelAdapter{contextResult: first}
	k := newTestKernel(t, workspace, bob)

	started, err := k.StartTask(context.Background(), StartInput{Goal: "preserve the first stable Bob capture"})
	if err != nil || !started.OK {
		t.Fatalf("start with Bob failed: env=%+v err=%v", started, err)
	}
	evidence, err := k.Store().Evidence(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	bobFacts := evidenceOfKind(evidence, domain.KindRepositoryContract)
	if len(bobFacts) != 1 || bobFacts[0].RawRef == "" {
		t.Fatalf("expected one Bob fact with stable raw, got %+v", bobFacts)
	}
	rawID := filepath.Base(bobFacts[0].RawRef)
	raw, err := k.Store().ReadRaw(started.TaskID, rawID)
	if err != nil || raw != first.Raw {
		t.Fatalf("first stable raw = %q, err=%v; want %q", raw, err, first.Raw)
	}

	second := bobContextFixtureResult(adapters.StatusAuthoritative)
	second.Raw = `{"capture":"second"}`
	bob.setContextResult(second)
	caseFile, err := k.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = k.orientWithBob(context.Background(), caseFile)

	raw, err = k.Store().ReadRaw(started.TaskID, rawID)
	if err != nil {
		t.Fatal(err)
	}
	if raw != first.Raw {
		t.Fatalf("retry overwrote retry-stable raw: got %q, want original %q", raw, first.Raw)
	}
	evidence, err = k.Store().Evidence(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(evidenceOfKind(evidence, domain.KindRepositoryContract)); got != 1 {
		t.Fatalf("retry duplicated repository-contract evidence: %d", got)
	}
	if got := bobRawFileCount(t, k, started.TaskID); got != 1 {
		t.Fatalf("retry changed stable raw file count: %d", got)
	}
}

func TestBobValidCompactTruncationHasNoMisleadingRetryAction(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	result := bobContextFixtureResult(adapters.StatusPartial)
	result.Facts[0].Attributes["bob_truncated"] = "true"
	result.Warnings = []string{"compact Bob context was truncated at its documented bound"}
	bob := &fakeAdapter{name: "bob", byOp: map[string]adapters.Result{"context": result}}
	k := newTestKernel(t, workspace, bob)

	env, err := k.StartTask(context.Background(), StartInput{Goal: "orient from bounded Bob context"})
	if err != nil || !env.OK || env.Phase != domain.PhaseInvestigating {
		t.Fatalf("valid compact truncation blocked orientation: env=%+v err=%v", env, err)
	}
	if !env.Degraded || !hasWarning(env.Warnings, "truncated") {
		t.Fatalf("valid truncation must remain explicit: %+v", env)
	}
	if len(evidenceOfKind(mustEvidence(t, k, env.TaskID), domain.KindRepositoryContract)) != 1 {
		t.Fatalf("valid bounded context was not retained: %+v", env)
	}
	if action := findAction(env.Actions, "bob_context"); action != nil {
		t.Fatalf("valid compact truncation emitted a misleading repair/retry action: %+v", action)
	}
}

func TestBobNonRegularManifestDegradesWithoutInvocation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "directory",
			setup: func(t *testing.T, workspace string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(workspace, "bob.yaml"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dangling symlink",
			setup: func(t *testing.T, workspace string) {
				t.Helper()
				if err := os.Symlink("missing-bob-manifest.yaml", filepath.Join(workspace, "bob.yaml")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testRepo(t)
			tc.setup(t, workspace)
			bob := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
			k := newTestKernel(t, workspace, bob)

			input := OpenInput{StartInput: StartInput{
				Goal: "degrade safely on an unsafe manifest node", IdempotencyKey: "invalid-bob-manifest-" + tc.name,
			}}
			env, err := k.OpenTask(context.Background(), input)
			if err != nil || !env.OK || env.Phase != domain.PhaseInvestigating {
				t.Fatalf("non-regular manifest blocked task: env=%+v err=%v", env, err)
			}
			if !env.Degraded || !hasWarning(env.Warnings, "bob.yaml") {
				t.Fatalf("non-regular manifest degradation was not explicit: %+v", env)
			}
			if action := findAction(env.Actions, "bob_context"); action == nil || len(action.BlockedBy) != 1 || action.BlockedBy[0] != "invalid bob.yaml" {
				t.Fatalf("expected blocked Bob repair action, got %+v", action)
			}
			if requests := bob.requests(); len(requests) != 0 {
				t.Fatalf("Bob was invoked for non-regular bob.yaml: %+v", requests)
			}
			facts := evidenceOfKind(mustEvidence(t, k, env.TaskID), domain.KindRepositoryContract)
			if len(facts) != 1 || facts[0].Confidence != domain.ConfidenceUnknown || !strings.Contains(facts[0].Claim, "not assessed") || !strings.HasPrefix(facts[0].Source.URI, "bob://manifest/local/") {
				t.Fatalf("unsafe manifest evidence = %+v", facts)
			}
			firstID := facts[0].ID
			retry, retryErr := k.OpenTask(context.Background(), input)
			if retryErr != nil || !retry.OK || !retry.Degraded {
				t.Fatalf("invalid-manifest retry = %+v err=%v", retry, retryErr)
			}
			facts = evidenceOfKind(mustEvidence(t, k, env.TaskID), domain.KindRepositoryContract)
			if len(facts) != 1 || facts[0].ID != firstID {
				t.Fatalf("invalid-manifest retry duplicated evidence: %+v", facts)
			}
		})
	}
}

func TestBobUnavailableAndInvalidManifestDegradeWithoutBlocking(t *testing.T) {
	for _, tc := range []struct {
		name      string
		result    adapters.Result
		blockedBy string
	}{
		{
			name:      "missing binary",
			result:    adapters.Result{Status: adapters.StatusUnavailable, Summary: "bob unavailable", Warnings: []string{"bob unavailable"}, Facts: []adapters.Fact{{Kind: "tool_unavailable", Claim: "Bob unavailable", Confidence: "unknown"}}},
			blockedBy: "Bob binary unavailable",
		},
		{
			name: "invalid manifest",
			result: adapters.Result{Status: adapters.StatusError, Summary: "manifest invalid", Warnings: []string{"manifest invalid"}, Facts: []adapters.Fact{{
				Kind: "repository_contract", Claim: "Bob rejected bob.yaml as invalid", Confidence: "unknown",
				Attributes: map[string]string{"error_code": "manifest_invalid"},
			}}},
			blockedBy: "invalid bob.yaml",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testRepo(t)
			writeBobManifest(t, workspace)
			bob := &fakeAdapter{name: "bob", down: tc.name == "missing binary", byOp: map[string]adapters.Result{"context": tc.result}}
			k := newTestKernel(t, workspace, bob)
			env, err := k.StartTask(context.Background(), StartInput{Goal: "continue honestly"})
			if err != nil || !env.OK || env.Phase != domain.PhaseInvestigating {
				t.Fatalf("Bob degradation blocked task: env=%+v err=%v", env, err)
			}
			if !env.Degraded || !hasWarning(env.Warnings, "Bob repository contract") {
				t.Fatalf("Bob degradation not explicit: %+v", env)
			}
			if hasWarning(env.Warnings, "verification on their surfaces") && hasWarning(env.Warnings, "bob") {
				t.Fatalf("Bob must not be described as a verification surface: %v", env.Warnings)
			}
			action := findAction(env.Actions, "bob_context")
			if action == nil || len(action.BlockedBy) != 1 || action.BlockedBy[0] != tc.blockedBy {
				t.Fatalf("expected corrective Bob context action blocked by %q, got %+v", tc.blockedBy, action)
			}
		})
	}
}

func TestBobPlanCancellationBeforeCommitPublishesNothing(t *testing.T) {
	workspace := testRepo(t)
	bob := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
	k := newTestKernel(t, workspace, bob)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "cancel a Bob-owned boundary review"})
	if err != nil || !started.OK {
		t.Fatalf("start failed: env=%+v err=%v", started, err)
	}
	writeBobManifest(t, workspace)

	beforeCase, err := k.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvidence := mustEvidence(t, k, started.TaskID)
	beforeRaw := bobRawFileCount(t, k, started.TaskID)
	ctx, cancel := context.WithCancel(context.Background())
	bob.setPathResult(func(req adapters.Request) adapters.Result {
		cancel()
		return bobPathFixtureResult(req.Str("path"), "outside_bob_ownership", nil)
	})

	planned, err := k.PlanContext(ctx, PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "the declared file needs a change", DisproveBy: "the ownership review rejects the boundary",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "the request may be canceled before publication",
	})
	if !errors.Is(err, context.Canceled) || planned.OK {
		t.Fatalf("canceled plan = %+v, err=%v; want context.Canceled rejection", planned, err)
	}
	afterCase, loadErr := k.Store().Load(started.TaskID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if afterCase.Status != domain.PhaseInvestigating || afterCase.Revision != beforeCase.Revision {
		t.Fatalf("canceled plan changed case snapshot: before=%+v after=%+v", beforeCase, afterCase)
	}
	if _, loadErr := k.Store().LoadPlan(started.TaskID); loadErr == nil {
		t.Fatal("canceled plan published plan.json")
	}
	afterEvidence := mustEvidence(t, k, started.TaskID)
	if len(afterEvidence) != len(beforeEvidence) || len(evidenceOfKind(afterEvidence, domain.KindRepositoryContract)) != 0 {
		t.Fatalf("canceled plan published evidence: before=%+v after=%+v", beforeEvidence, afterEvidence)
	}
	if got := bobRawFileCount(t, k, started.TaskID); got != beforeRaw {
		t.Fatalf("canceled plan published raw capture: before=%d after=%d", beforeRaw, got)
	}
	pathCalls := 0
	for _, req := range bob.requests() {
		if req.Operation == "path" {
			pathCalls++
		}
	}
	if pathCalls != 1 {
		t.Fatalf("cancellation test did not reach the pre-commit boundary: path calls=%d", pathCalls)
	}
}

func TestBobPartialPathWithoutClassificationStopsBoundaryBudget(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	partial := adapters.Result{
		Status:   adapters.StatusPartial,
		Summary:  "Bob could not classify the requested path",
		Warnings: []string{"path classification unavailable"},
		Facts: []adapters.Fact{{
			Kind: "repository_contract", Claim: "Bob path classification is unavailable", Confidence: "unknown",
			Attributes: map[string]string{"error_code": "path_unclassified"},
		}},
		Raw: `{"schema_version":1,"ok":false,"error":{"code":"path_unclassified"}}`,
	}
	bob := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
	bob.setPathResult(func(adapters.Request) adapters.Result { return partial })
	k := newTestKernel(t, workspace, bob)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "stop on an unclassified Bob path"})
	if err != nil || !started.OK {
		t.Fatalf("start failed: env=%+v err=%v", started, err)
	}
	beforeEvidence := mustEvidence(t, k, started.TaskID)
	beforeRaw := bobRawFileCount(t, k, started.TaskID)

	planned, err := k.PlanContext(context.Background(), PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "one of the files needs a change", DisproveBy: "Bob ownership makes the edit inappropriate",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go", "internal/generated/other.go"}},
		Uncertainty:    "Bob may be unable to classify a path",
	})
	if err != nil || !planned.OK {
		t.Fatalf("advisory Bob degradation blocked plan: env=%+v err=%v", planned, err)
	}
	if !planned.Degraded || !hasWarning(planned.Warnings, "remain unclassified") {
		t.Fatalf("unclassified partial result was not exposed honestly: %+v", planned)
	}
	if action := findAction(planned.Actions, "bob_path"); action == nil || action.Arguments["path"] != "src/callback.go" || len(action.BlockedBy) != 1 {
		t.Fatalf("unclassified path needs one blocked corrective action: %+v", action)
	}
	pathCalls := 0
	for _, req := range bob.requests() {
		if req.Operation == "path" {
			pathCalls++
		}
	}
	if pathCalls != 1 {
		t.Fatalf("partial unclassified result consumed more path budget: calls=%d", pathCalls)
	}
	afterEvidence := mustEvidence(t, k, started.TaskID)
	if len(afterEvidence) != len(beforeEvidence) {
		t.Fatalf("unclassified partial result was retained as evidence: before=%+v after=%+v", beforeEvidence, afterEvidence)
	}
	if got := bobRawFileCount(t, k, started.TaskID); got != beforeRaw {
		t.Fatalf("unclassified partial result was retained as raw: before=%d after=%d", beforeRaw, got)
	}
}

func TestBobManagedBoundaryWarnsAndPreservesReadOnlyActions(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	bob := &fakeAdapter{name: "bob", byOp: map[string]adapters.Result{
		"context": bobContextFixtureResult(adapters.StatusAuthoritative),
		"path":    bobPathFixtureResult("src/callback.go", "will_conflict", []string{"add-cli-command"}),
	}}
	k := newTestKernel(t, workspace, bob)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "change managed path"})
	planned, err := k.PlanContext(context.Background(), PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "change is needed", DisproveBy: "inspect generated ownership"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "Bob may own the path",
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan failed: %+v err=%v", planned, err)
	}
	if !hasWarning(planned.Warnings, "Bob-managed") || len(planned.Facts) != 1 || planned.Facts[0].Kind != domain.KindRepositoryContract {
		t.Fatalf("managed path warning/fact missing: %+v", planned)
	}
	if planned.Facts[0].Kind.CanVerify() {
		t.Fatal("Bob plan fact must remain non-verifying")
	}
	pathAction := findAction(planned.Actions, "bob_path")
	if pathAction == nil || pathAction.Arguments["workspace"] != workspace || pathAction.Arguments["path"] != "src/callback.go" {
		t.Fatalf("exact bob_path action missing: %+v", pathAction)
	}
	if pathAction.Command != bobCommand("--json", "path", "--workspace", workspace, "--", "src/callback.go") {
		t.Fatalf("unexpected Bob path command: %q", pathAction.Command)
	}
	playbook := findAction(planned.Actions, "bob_playbook")
	if playbook == nil || playbook.Arguments["id"] != "add-cli-command" || playbook.Arguments["operation"] != "show" {
		t.Fatalf("Bob-returned playbook was not preserved exactly: %+v", playbook)
	}
	for _, request := range bob.requests() {
		if request.Operation == "apply" {
			t.Fatal("Cortex must never execute Bob apply")
		}
	}
}

func TestBobHumanExtensionPathDoesNotWarn(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	bob := &fakeAdapter{name: "bob", byOp: map[string]adapters.Result{
		"context": bobContextFixtureResult(adapters.StatusAuthoritative),
		"path":    bobPathFixtureResult("internal/cli/hello.go", "outside_bob_ownership", []string{"add-cli-command"}),
	}}
	k := newTestKernel(t, workspace, bob)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "add extension"})
	planned, _ := k.PlanContext(context.Background(), PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "extension is needed", DisproveBy: "inspect ownership"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"internal/cli/hello.go"}}, Uncertainty: "application safety is separate from Bob ownership",
	})
	if !planned.OK {
		t.Fatalf("plan failed: %+v", planned)
	}
	if hasWarning(planned.Warnings, "Bob-managed") || hasWarning(planned.Warnings, "safe") || findAction(planned.Actions, "bob_path") != nil {
		t.Fatalf("human extension path must not receive an ownership warning/action: %+v", planned)
	}
	if playbook := findAction(planned.Actions, "bob_playbook"); playbook == nil || playbook.Arguments["id"] != "add-cli-command" {
		t.Fatalf("Bob-returned extension playbook should remain available without an ownership warning: %+v", planned.Actions)
	}
}

func TestBobPathBudgetAndDedupe(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	bob := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
	bob.setPathResult(func(req adapters.Request) adapters.Result {
		return bobPathFixtureResult(req.Str("path"), "outside_bob_ownership", nil)
	})
	k := newTestKernel(t, workspace, bob)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "bounded path audit"})
	files := []string{"src/callback.go", "src/callback.go"}
	for i := 0; i < maxBobPathCalls+4; i++ {
		files = append(files, fmt.Sprintf("internal/generated/file-%02d.go", i))
	}
	planned, _ := k.PlanContext(context.Background(), PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "many files may change", DisproveBy: "inspect the bounded path set"}},
		ChangeBoundary: domain.ChangeBoundary{Files: files}, Uncertainty: "ownership review is budgeted",
	})
	if !planned.OK || !planned.Degraded || !hasWarning(planned.Warnings, "capped at 16 calls") {
		t.Fatalf("path budget must be explicit: %+v", planned)
	}
	pathCalls := 0
	seen := map[string]bool{}
	for _, request := range bob.requests() {
		if request.Operation != "path" {
			continue
		}
		pathCalls++
		path := request.Str("path")
		if seen[path] {
			t.Fatalf("duplicate Bob path call for %s", path)
		}
		seen[path] = true
	}
	if pathCalls != maxBobPathCalls {
		t.Fatalf("Bob path call budget = %d, want %d", pathCalls, maxBobPathCalls)
	}
}

func TestBobBoundaryWarningKeepsRiskBeforeLongPath(t *testing.T) {
	path := strings.Repeat("deep/", 500) + "generated.go"
	for _, test := range []struct {
		effect, risk string
	}{
		{"will_conflict", "Bob-managed"},
		{"reserved_for_bob", "Bob-reserved"},
		{"requires_manifest_change", "Bob manifest-controlled"},
		{"unsafe", "Bob-unsafe"},
	} {
		warning, _ := bobBoundaryWarning(path, test.effect)
		bounded := boundedBobWarnings([]string{warning})
		if len(bounded) != 1 || len(bounded[0]) > maxBobPlanNoteBytes || !strings.HasPrefix(bounded[0], test.risk) {
			t.Fatalf("%s warning lost risk under path bound: %q", test.effect, bounded)
		}
		if !strings.Contains(bounded[0], "…") {
			t.Fatalf("%s warning did not mark the bounded path: %q", test.effect, bounded[0])
		}
	}
}

func TestBobRepositoryContractCannotSatisfyBehavioralVerification(t *testing.T) {
	workspace := testRepo(t)
	writeBobManifest(t, workspace)
	bob := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
	bob.setPathResult(func(req adapters.Request) adapters.Result {
		return bobPathFixtureResult(req.Str("path"), "outside_bob_ownership", nil)
	})
	k := newTestKernel(t, workspace, bob)
	started, err := k.StartTask(context.Background(), StartInput{
		Goal: "prove browser behavior independently of repository ownership", Risk: "low",
		Surfaces: []domain.Surface{domain.SurfaceBrowser},
	})
	if err != nil || !started.OK {
		t.Fatalf("start failed: env=%+v err=%v", started, err)
	}
	planned, err := k.PlanContext(context.Background(), PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "the browser callback works", DisproveBy: "the exact browser flow fails",
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Verification:   []string{"cairntrace_flow"},
		Uncertainty:    "repository ownership says nothing about runtime browser behavior",
	})
	if err != nil || !planned.OK {
		t.Fatalf("plan failed: env=%+v err=%v", planned, err)
	}
	bobEvidence := evidenceOfKind(mustEvidence(t, k, started.TaskID), domain.KindRepositoryContract)
	if len(bobEvidence) == 0 {
		t.Fatal("test precondition failed: no Bob repository-contract evidence")
	}
	bobIDs := make(map[string]bool, len(bobEvidence))
	for _, evidence := range bobEvidence {
		bobIDs[evidence.ID] = true
	}

	verified, err := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, DisableAutoSpecs: true, NoOpAcknowledged: true,
		ClaimSpecs: []domain.VerificationClaim{{
			ID: "browser_callback", Statement: "the browser callback returns to the application",
			Surface: domain.SurfaceBrowser, Verifier: "cairntrace", Contract: "specs/callback.yml",
		}},
	})
	if err != nil || !verified.OK {
		t.Fatalf("verification run failed: env=%+v err=%v", verified, err)
	}
	receipts, err := k.Store().Verifications(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	claims := latestReceipts(receipts, domain.VerificationPurposeNamedClaim)
	if len(claims) != 1 || claims[0].Status != domain.VerifyNotRun || claims[0].Proven() {
		t.Fatalf("Bob evidence satisfied a behavioral claim: %+v", claims)
	}
	for _, receipt := range receipts {
		for _, evidenceID := range receipt.Evidence {
			if bobIDs[evidenceID] {
				t.Fatalf("behavioral receipt linked repository-contract evidence as proof: %+v", receipt)
			}
		}
	}
	if assessment := assessVerification([]string{"cairntrace_flow"}, receipts); assessment.Outcome == VerificationVerified {
		t.Fatalf("repository-contract evidence made behavioral verification green: %+v", assessment)
	}
}

func evidenceOfKind(evidence []domain.Evidence, kind domain.EvidenceKind) []domain.Evidence {
	var out []domain.Evidence
	for _, item := range evidence {
		if item.Kind == kind {
			out = append(out, item)
		}
	}
	return out
}

func factViewsOfKind(facts []domain.FactView, kind domain.EvidenceKind) []domain.FactView {
	var out []domain.FactView
	for _, fact := range facts {
		if fact.Kind == kind {
			out = append(out, fact)
		}
	}
	return out
}

func TestBobPlanRejectsCrossWorkspaceTaskBeforeClassification(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	root := t.TempDir()
	workspaceA := filepath.Join(root, "left", "shared-name")
	workspaceB := filepath.Join(root, "right", "shared-name")
	for _, workspace := range []string{workspaceA, workspaceB} {
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		writeBobManifest(t, workspace)
	}
	cfgA, cfgB := config.For(workspaceA), config.For(workspaceB)
	if cfgA.CasesDir != cfgB.CasesDir {
		t.Fatalf("test precondition failed: same basename stores differ: %s != %s", cfgA.CasesDir, cfgB.CasesDir)
	}
	storeA, err := casefs.New(cfgA.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	bobA := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
	kA := NewWith(cfgA, storeA, adapters.NewRegistry(adapters.NewGit(), bobA))
	started, err := kA.StartTask(context.Background(), StartInput{Goal: "keep Bob provenance in workspace A"})
	if err != nil || !started.OK {
		t.Fatalf("start workspace A = %+v err=%v", started, err)
	}

	storeB, err := casefs.New(cfgB.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	bobB := &bobKernelAdapter{contextResult: bobContextFixtureResult(adapters.StatusAuthoritative)}
	bobB.setPathResult(func(req adapters.Request) adapters.Result {
		return bobPathFixtureResult(req.Str("path"), "will_conflict", nil)
	})
	kB := NewWith(cfgB, storeB, adapters.NewRegistry(adapters.NewGit(), bobB))
	planned, err := kB.PlanContext(context.Background(), PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "the file needs a change", DisproveBy: "the boundary belongs elsewhere"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "workspace identity must remain authoritative",
	})
	if err != nil || planned.OK || !strings.Contains(planned.Error, "different workspace") {
		t.Fatalf("cross-workspace plan = %+v err=%v", planned, err)
	}
	for _, req := range bobB.requests() {
		if req.Operation == "path" {
			t.Fatalf("cross-workspace task reached Bob classification: %+v", req)
		}
	}
	durable, err := storeA.Load(started.TaskID)
	if err != nil || durable.Status != domain.PhaseInvestigating {
		t.Fatalf("cross-workspace rejection changed case: %+v err=%v", durable, err)
	}
	if _, err := storeA.LoadPlan(started.TaskID); !errors.Is(err, casefs.ErrNotFound) {
		t.Fatalf("cross-workspace rejection published a plan: %v", err)
	}
}

func mustEvidence(t *testing.T, k *Kernel, taskID string) []domain.Evidence {
	t.Helper()
	evidence, err := k.Store().Evidence(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func bobRawFileCount(t *testing.T, k *Kernel, taskID string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(k.Store().Root(), taskID, "raw"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func findAction(actions []domain.NextAction, tool string) *domain.NextAction {
	for i := range actions {
		if actions[i].Tool == tool {
			return &actions[i]
		}
	}
	return nil
}
