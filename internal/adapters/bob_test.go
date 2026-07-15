package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

const bobFixtureWorkspace = "/workspace"

type bobRunnerCall struct {
	dir  string
	bin  string
	args []string
}

type recordingBobRunner struct {
	mu     sync.Mutex
	stdout string
	stderr string
	exit   int
	err    error
	calls  []bobRunnerCall
}

func (r *recordingBobRunner) run(_ context.Context, dir, bin string, args ...string) ([]byte, []byte, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, bobRunnerCall{dir: dir, bin: bin, args: append([]string(nil), args...)})
	return []byte(r.stdout), []byte(r.stderr), r.exit, r.err
}

func (r *recordingBobRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingBobRunner) lastCall(t *testing.T) bobRunnerCall {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("Bob runner was not called")
	}
	return r.calls[len(r.calls)-1]
}

func testBob(r runner) *Bob {
	return &Bob{tool: tool{
		// git is a hard dependency and therefore a stable PATH sentinel. The
		// injected runner still intercepts every process invocation.
		bin: "git", run: r, redact: redact.New(), timeout: time.Second, retries: 0,
	}}
}

func TestBobHealthUsesVersionedJSONArgv(t *testing.T) {
	r := &recordingBobRunner{stdout: bobSuccessJSON(t, "version", json.RawMessage(`{"name":"bob","version":"v0.4.0","commit":"8639e51","date":"2026-07-15"}`))}
	b := testBob(r)
	if err := b.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	call := r.lastCall(t)
	if call.dir != "" || !reflect.DeepEqual(call.args, []string{"--json", "version"}) {
		t.Fatalf("health call = dir %q args %q", call.dir, call.args)
	}
	if !reflect.DeepEqual(b.Capabilities(), []Capability{CapabilityRepositoryContract}) {
		t.Fatalf("capabilities = %v", b.Capabilities())
	}
}

func TestBobContextPublicFixtures(t *testing.T) {
	tests := []struct {
		name, fixture, state string
		warning              bool
	}{
		{"clean", "context-clean-v1.json", "clean", false},
		{"drifted", "context-drift-v1.json", "drifted", true},
		{"conflicted", "context-conflict-v1.json", "conflicted", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bobPublicPayload(t, test.fixture, "context")
			r := &recordingBobRunner{stdout: bobSuccessJSON(t, "context", data)}
			result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusAuthoritative || len(result.Facts) != 1 {
				t.Fatalf("context result = %#v", result)
			}
			fact := result.Facts[0]
			if fact.Kind != bobRepositoryFactKind || fact.Confidence != "high" || fact.Attributes["repository_state"] != test.state || fact.Attributes["schema_version"] != "1" || fact.Attributes["profile"] != "compact" || fact.Attributes["bob_truncated"] != "false" {
				t.Fatalf("context fact = %#v", fact)
			}
			if fact.Attributes["workspace"] != bobFixtureWorkspace || fact.Attributes["recipe_id"] != "go-agent-tool" || fact.Attributes["recipe_version"] != "4" || !strings.HasPrefix(fact.URI, "bob://context/v1/") {
				t.Fatalf("context provenance = %#v", fact)
			}
			for _, field := range []string{"contract_digest=", "context_digest=", "plan_digest_version=1", "plan_digest="} {
				if !strings.Contains(fact.Claim, field) {
					t.Fatalf("context durable claim %q lacks %q", fact.Claim, field)
				}
			}
			if test.warning != containsBobWarning(result.Warnings, test.state) {
				t.Fatalf("warnings = %q, want state warning %t", result.Warnings, test.warning)
			}
			call := r.lastCall(t)
			want := []string{"--json", "context", bobFixtureWorkspace, "--profile", "compact"}
			if call.dir != bobFixtureWorkspace || !reflect.DeepEqual(call.args, want) {
				t.Fatalf("context call = dir %q args %q, want %q", call.dir, call.args, want)
			}
			if result.Raw == "" || strings.Contains(result.Summary, result.Raw) {
				t.Fatalf("raw should be retained separately, result = %#v", result)
			}
		})
	}
}

func TestBobPathPublicFixturesAndExactArgv(t *testing.T) {
	tests := []struct {
		name, fixture, path, classification, effect, extensions, playbooks string
	}{
		{"managed", "path-managed-v1.json", "internal/cli/root.go", "managed", "will_conflict", `[]`, `["add-cli-command"]`},
		{"extension", "path-extension-v1.json", "internal/cli/hello.go", "extension_point", "outside_bob_ownership", `["cli.command_files"]`, `["add-cli-command"]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bobPublicPayload(t, test.fixture, "path")
			r := &recordingBobRunner{stdout: bobSuccessJSON(t, "path", data)}
			result, err := testBob(r).Execute(context.Background(), Request{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": test.path}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusAuthoritative || len(result.Facts) != 1 {
				t.Fatalf("path result = %#v", result)
			}
			fact := result.Facts[0]
			if fact.Kind != bobRepositoryFactKind || fact.Confidence != "high" || fact.Location == nil || fact.Location.File != test.path || fact.Attributes["classification"] != test.classification || fact.Attributes["human_edit_effect"] != test.effect || fact.Attributes["extension_points"] != test.extensions || fact.Attributes["related_playbooks"] != test.playbooks {
				t.Fatalf("path fact = %#v", fact)
			}
			if test.name == "managed" && fact.Attributes["artifact_id"] != "cli.root" {
				t.Fatalf("artifact attribute = %q", fact.Attributes["artifact_id"])
			}
			if !strings.Contains(fact.Claim, "related_playbooks=") {
				t.Fatalf("path durable claim lacks playbooks: %q", fact.Claim)
			}
			if test.name == "managed" && !strings.Contains(fact.Claim, "artifact=cli.root") {
				t.Fatalf("path durable claim lacks artifact: %q", fact.Claim)
			}
			if test.name == "extension" && !strings.Contains(fact.Claim, "extension_points=") {
				t.Fatalf("path durable claim lacks extension points: %q", fact.Claim)
			}
			call := r.lastCall(t)
			want := []string{"--json", "path", "--workspace", bobFixtureWorkspace, "--", test.path}
			if call.dir != bobFixtureWorkspace || !reflect.DeepEqual(call.args, want) {
				t.Fatalf("path call = dir %q args %q, want %q", call.dir, call.args, want)
			}
		})
	}
}

func TestBobPathReservedAndUnsafeVocabulary(t *testing.T) {
	tests := []struct {
		name, path, classification, state, effect string
	}{
		{"reserved", "bob.lock", "reserved", "reserved", "reserved_for_bob"},
		{"unsafe", "tmp/socket", "unmanaged", "special_file", "unsafe"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bobPathFixtureData(t, "path-extension-v1.json")
			data.Path, data.Classification, data.State, data.HumanEditEffect = test.path, test.classification, test.state, test.effect
			data.Exists = true
			data.ExtensionPoints, data.RelatedPlaybooks, data.Actions = []string{}, []string{}, []bobAction{}
			r := &recordingBobRunner{stdout: bobSuccessJSON(t, "path", mustBobJSON(t, data))}
			result, err := testBob(r).Execute(context.Background(), Request{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": test.path}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusAuthoritative || result.Facts[0].Attributes["human_edit_effect"] != test.effect {
				t.Fatalf("path result = %#v", result)
			}
		})
	}
}

func TestBobRejectsIncoherentPathSemanticTuples(t *testing.T) {
	tests := []struct {
		name, classification, state, effect string
	}{
		{"unmanaged with managed state", "unmanaged", "managed_in_sync", "outside_bob_ownership"},
		{"missing with conflict effect", "missing", "unmanaged_missing", "will_conflict"},
		{"managed with outside effect", "managed", "managed_in_sync", "outside_bob_ownership"},
		{"reserved with unmanaged state", "reserved", "unmanaged_present", "reserved_for_bob"},
		{"retired ownership with conflict effect", "managed", "retired_owned", "will_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bobPathFixtureData(t, "path-extension-v1.json")
			data.Path = "candidate.go"
			data.Classification, data.State, data.HumanEditEffect = test.classification, test.state, test.effect
			data.ExtensionPoints, data.RelatedPlaybooks, data.Actions = []string{}, []string{}, []bobAction{}
			r := &recordingBobRunner{stdout: bobSuccessJSON(t, "path", mustBobJSON(t, data))}
			result, err := testBob(r).Execute(context.Background(), Request{
				Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": data.Path},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusError || len(result.Facts) != 0 || !strings.Contains(result.Summary, "internally inconsistent") {
				t.Fatalf("incoherent tuple was trusted: %#v", result)
			}
		})
	}
}

func TestBobFailureEnvelopeIsTypedAndUnknownConfidence(t *testing.T) {
	failure := bobFailureJSON(t, "context", "manifest_invalid", "context: parse bob.yaml: bad value")
	r := &recordingBobRunner{stdout: failure, exit: 2}
	result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPartial || len(result.Facts) != 1 || result.Facts[0].Confidence != "unknown" {
		t.Fatalf("failure result = %#v", result)
	}
	attrs := result.Facts[0].Attributes
	if attrs["error_code"] != "manifest_invalid" || attrs["command"] != "context" || attrs["workspace"] != bobFixtureWorkspace || attrs["error_message"] == "" {
		t.Fatalf("failure attributes = %#v", attrs)
	}
}

func TestBobRejectsInvalidEnvelopeAndFutureSchemas(t *testing.T) {
	contextData := bobPublicPayload(t, "context-clean-v1.json", "context")
	tests := []struct {
		name   string
		stdout string
		exit   int
	}{
		{"unknown outer field", strings.TrimSuffix(bobSuccessJSON(t, "context", contextData), "}") + `,"future":true}`, 0},
		{"future outer schema", strings.Replace(bobSuccessJSON(t, "context", contextData), `"schema_version":1`, `"schema_version":2`, 1), 0},
		{"multiple documents", bobSuccessJSON(t, "context", contextData) + `{}`, 0},
		{"truncated", bobSuccessJSON(t, "context", contextData) + "\n…(truncated)", 0},
		{"ok with failing exit", bobSuccessJSON(t, "context", contextData), 2},
		{"error with zero exit", bobFailureJSON(t, "context", "manifest_invalid", "bad manifest"), 0},
		{"unknown error code", bobFailureJSON(t, "context", "future_error", "future failure"), 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &recordingBobRunner{stdout: test.stdout, exit: test.exit}
			result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusError || len(result.Facts) != 0 {
				t.Fatalf("invalid output was trusted: %#v", result)
			}
			if len(result.Raw) > rawBackstop+32 {
				t.Fatalf("raw exceeded hard bound: %d", len(result.Raw))
			}
		})
	}

	// Use Bob's published future-schema fixture so the rejection test remains
	// bound to BOB-5 instead of synthesizing a second interpretation locally.
	futureContext := bobPublicPayload(t, "error-unsupported-schema-v1.json", "context")
	r := &recordingBobRunner{stdout: bobSuccessJSON(t, "context", futureContext)}
	result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil || result.Status != StatusError || !strings.Contains(result.Summary, "schema version 2") {
		t.Fatalf("future context schema result = %#v err=%v", result, err)
	}

	data := map[string]any{}
	if err := json.Unmarshal(contextData, &data); err != nil {
		t.Fatal(err)
	}
	data["future_field"] = true
	r = &recordingBobRunner{stdout: bobSuccessJSON(t, "context", mustBobJSON(t, data))}
	result, err = testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil || result.Status != StatusError || !strings.Contains(result.Summary, "unknown field") {
		t.Fatalf("unknown context field result = %#v err=%v", result, err)
	}
}

func TestBobRejectsWrongWorkspaceAndPath(t *testing.T) {
	contextData := bobPublicPayload(t, "context-clean-v1.json", "context")
	var contextValue bobContextData
	if err := json.Unmarshal(contextData, &contextValue); err != nil {
		t.Fatal(err)
	}
	contextValue.Workspace = "/other"
	r := &recordingBobRunner{stdout: bobSuccessJSON(t, "context", mustBobJSON(t, contextValue))}
	result, _ := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if result.Status != StatusError || !strings.Contains(result.Summary, "workspace") {
		t.Fatalf("wrong workspace result = %#v", result)
	}

	pathValue := bobPathFixtureData(t, "path-managed-v1.json")
	pathValue.Path = "other.go"
	r = &recordingBobRunner{stdout: bobSuccessJSON(t, "path", mustBobJSON(t, pathValue))}
	result, _ = testBob(r).Execute(context.Background(), Request{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": "internal/cli/root.go"}})
	if result.Status != StatusError || !strings.Contains(result.Summary, "want") {
		t.Fatalf("wrong path result = %#v", result)
	}
}

func TestBobTimeoutDegradesUnavailable(t *testing.T) {
	r := &recordingBobRunner{err: context.DeadlineExceeded}
	result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnavailable || len(result.Facts) != 1 || result.Facts[0].Kind != "tool_unavailable" {
		t.Fatalf("timeout result = %#v", result)
	}
}

func TestBobCallerCancellationPropagatesWithoutUnavailableFact(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		input     map[string]any
		ctxErr    error
		runErr    error
	}{
		{name: "context canceled", operation: "context", input: map[string]any{"workspace": bobFixtureWorkspace}, ctxErr: context.Canceled},
		{name: "context deadline", operation: "context", input: map[string]any{"workspace": bobFixtureWorkspace}, ctxErr: context.DeadlineExceeded, runErr: context.DeadlineExceeded},
		{name: "path canceled", operation: "path", input: map[string]any{"workspace": bobFixtureWorkspace, "path": "internal/cli/root.go"}, ctxErr: context.Canceled},
		{name: "path deadline", operation: "path", input: map[string]any{"workspace": bobFixtureWorkspace, "path": "internal/cli/root.go"}, ctxErr: context.DeadlineExceeded, runErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if errors.Is(tc.ctxErr, context.Canceled) {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
			}
			r := &recordingBobRunner{err: tc.runErr}
			result, err := testBob(r).Execute(ctx, Request{Operation: tc.operation, Input: tc.input})
			if !errors.Is(err, tc.ctxErr) {
				t.Fatalf("error = %v, want %v", err, tc.ctxErr)
			}
			if result.Status != "" || len(result.Facts) != 0 {
				t.Fatalf("caller cancellation became adapter evidence: %#v", result)
			}
		})
	}
}

func TestBobDeclaredTruncationIsPartial(t *testing.T) {
	contextValue := bobContextFixtureData(t, "context-clean-v1.json")
	contextValue.Truncation.Truncated = true
	contextValue.Truncation.Omitted = map[string]int{"playbooks": 1}
	r := &recordingBobRunner{stdout: bobSuccessJSON(t, "context", mustBobJSON(t, contextValue))}
	contextResult, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil {
		t.Fatal(err)
	}
	if contextResult.Status != StatusPartial || !containsText(contextResult.Warnings, "context output was truncated") || contextResult.Facts[0].Attributes["bob_truncated"] != "true" {
		t.Fatalf("truncated context result = %#v", contextResult)
	}

	pathValue := bobPathFixtureData(t, "path-managed-v1.json")
	pathValue.Truncation.Truncated = true
	pathValue.Truncation.Omitted = map[string]int{"actions": 1}
	r = &recordingBobRunner{stdout: bobSuccessJSON(t, "path", mustBobJSON(t, pathValue))}
	pathResult, err := testBob(r).Execute(context.Background(), Request{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": pathValue.Path}})
	if err != nil {
		t.Fatal(err)
	}
	if pathResult.Status != StatusPartial || !containsText(pathResult.Warnings, "path output was truncated") || pathResult.Facts[0].Attributes["bob_truncated"] != "true" {
		t.Fatalf("truncated path result = %#v", pathResult)
	}
}

func TestBobAbsentDegradesWithoutFabricating(t *testing.T) {
	b := &Bob{tool: tool{bin: "definitely-not-a-real-bob-binary", run: &recordingBobRunner{}, redact: redact.New()}}
	if err := b.Health(context.Background()); !errors.Is(err, ErrToolMissing) {
		t.Fatalf("health error = %v, want ErrToolMissing", err)
	}
	result, err := b.Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnavailable || len(result.Facts) != 1 || result.Facts[0].Kind != "tool_unavailable" {
		t.Fatalf("missing Bob result = %#v", result)
	}
}

func TestBobRawHardBoundMarkerIsRejectedAndRetainedBounded(t *testing.T) {
	oversized := []byte(strings.Repeat("x", rawBackstop+1024))
	bounded := capBytes(oversized, rawBackstop)
	r := &recordingBobRunner{stdout: string(bounded)}
	result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusError || !strings.Contains(result.Summary, "envelope exceeds") {
		t.Fatalf("bounded raw result = %#v", result)
	}
	if len(result.Raw) > rawBackstop+len("\n…(truncated)") {
		t.Fatalf("retained raw length = %d", len(result.Raw))
	}
}

func TestBobEnvelopeAndContinuationBounds(t *testing.T) {
	data := bobPublicPayload(t, "context-clean-v1.json", "context")
	tooMany := make([]string, bobEnvelopeListLimit+1)
	for i := range tooMany {
		tooMany[i] = "warning"
	}
	ok := true
	tests := []struct {
		name   string
		stdout string
	}{
		{"envelope", strings.Repeat("x", bobEnvelopeByteLimit+1)},
		{"warnings count", string(mustBobJSON(t, bobCLIEnvelope{SchemaVersion: 1, OK: &ok, Command: "context", Data: data, Warnings: tooMany, NextActions: []string{}}))},
		{"warning size", string(mustBobJSON(t, bobCLIEnvelope{SchemaVersion: 1, OK: &ok, Command: "context", Data: data, Warnings: []string{strings.Repeat("x", bobEnvelopeStringLimit+1)}, NextActions: []string{}}))},
		{"next action size", string(mustBobJSON(t, bobCLIEnvelope{SchemaVersion: 1, OK: &ok, Command: "context", Data: data, Warnings: []string{}, NextActions: []string{strings.Repeat("x", bobEnvelopeStringLimit+1)}}))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &recordingBobRunner{stdout: test.stdout}
			result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusError || len(result.Summary) > 256 || len(strings.Join(result.Warnings, "")) > 1024 {
				t.Fatalf("unbounded envelope result = %#v", result)
			}
		})
	}
}

func TestBobWorkspaceComparisonResolvesSymlinksBestEffort(t *testing.T) {
	realWorkspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realWorkspace, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	value := bobContextFixtureData(t, "context-drift-v1.json")
	value.Workspace = alias
	value.Actions[0].CWD = alias
	r := &recordingBobRunner{stdout: bobSuccessJSON(t, "context", mustBobJSON(t, value))}
	result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": alias}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAuthoritative {
		t.Fatalf("canonical workspace result = %#v", result)
	}
	call := r.lastCall(t)
	canonical := canonicalBobWorkspace(realWorkspace)
	if call.dir != canonical || call.args[2] != canonical {
		t.Fatalf("canonical argv = dir %q args %q, want %q", call.dir, call.args, canonical)
	}
}

func TestBobRedactsRawAndWarnings(t *testing.T) {
	token := "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a"
	data := bobPublicPayload(t, "context-clean-v1.json", "context")
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		OK            bool            `json:"ok"`
		Command       string          `json:"command"`
		Data          json.RawMessage `json:"data"`
		Warnings      []string        `json:"warnings"`
		NextActions   []string        `json:"next_actions"`
	}
	if err := json.Unmarshal([]byte(bobSuccessJSON(t, "context", data)), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Warnings = []string{"token " + token}
	r := &recordingBobRunner{stdout: string(mustBobJSON(t, envelope)), stderr: "token " + token}
	result, err := testBob(r).Execute(context.Background(), Request{Operation: "context", Input: map[string]any{"workspace": bobFixtureWorkspace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAuthoritative || strings.Contains(result.Raw, token) || strings.Contains(strings.Join(result.Warnings, " "), token) || !strings.Contains(result.Raw, redact.Mask) {
		t.Fatalf("secret was not safely redacted: %#v", result)
	}
}

func TestBobNeverExecutesMutationOrUnknownOperations(t *testing.T) {
	r := &recordingBobRunner{}
	b := testBob(r)
	for _, operation := range []string{"apply", "plan", "check", "playbook", ""} {
		result, err := b.Execute(context.Background(), Request{Operation: operation, Input: map[string]any{"workspace": bobFixtureWorkspace}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != StatusError {
			t.Fatalf("operation %q status = %s", operation, result.Status)
		}
	}
	if r.callCount() != 0 {
		t.Fatalf("forbidden operations executed runner %d times", r.callCount())
	}
}

func TestBobRejectsUnsafeInputWithoutExecuting(t *testing.T) {
	r := &recordingBobRunner{}
	b := testBob(r)
	inputs := []Request{
		{Operation: "context", Input: map[string]any{"workspace": "relative"}},
		{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": "../escape"}},
		{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": "/absolute"}},
		{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": "--help"}},
	}
	for _, input := range inputs {
		result, err := b.Execute(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if input.Str("path") == "--help" {
			// A dash-prefixed repository filename is safe because exact argv uses
			// an option terminator. Its fake execution then fails JSON decoding.
			if result.Status != StatusError || r.callCount() != 1 {
				t.Fatalf("dash path result = %#v calls=%d", result, r.callCount())
			}
			continue
		}
		if result.Status != StatusError {
			t.Fatalf("unsafe input result = %#v", result)
		}
	}
	if r.callCount() != 1 {
		t.Fatalf("unsafe inputs unexpectedly executed runner %d times", r.callCount())
	}
}

func TestBobPublicFixtureAttributionAndIntegrity(t *testing.T) {
	type attribution struct {
		SourceTag      string            `json:"source_tag"`
		SourceRevision string            `json:"source_revision"`
		Files          map[string]string `json:"files"`
	}
	var source attribution
	data, err := os.ReadFile(filepath.Join("testdata", "bob", "v1", "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	if source.SourceTag != "v0.4.0" || source.SourceRevision != "8639e51828ec1511ba6745a67b31e622470fa837" || len(source.Files) != 6 {
		t.Fatalf("fixture attribution = %#v", source)
	}
	for name, expected := range source.Files {
		fixture, err := os.ReadFile(filepath.Join("testdata", "bob", "v1", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(fixture)
		actual := "sha256:" + hex.EncodeToString(digest[:])
		if actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

func bobPublicPayload(t *testing.T, name, field string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "bob", "v1", name))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	payload := fixture[field]
	if len(payload) == 0 {
		t.Fatalf("fixture %s has no %s payload", name, field)
	}
	return payload
}

func bobPathFixtureData(t *testing.T, name string) bobPathData {
	t.Helper()
	var data bobPathData
	if err := json.Unmarshal(bobPublicPayload(t, name, "path"), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func bobContextFixtureData(t *testing.T, name string) bobContextData {
	t.Helper()
	var data bobContextData
	if err := json.Unmarshal(bobPublicPayload(t, name, "context"), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func bobSuccessJSON(t *testing.T, command string, data json.RawMessage) string {
	t.Helper()
	ok := true
	return string(mustBobJSON(t, bobCLIEnvelope{
		SchemaVersion: bobSchemaVersion, OK: &ok, Command: command, Data: data,
		Warnings: []string{}, NextActions: []string{},
	}))
}

func bobFailureJSON(t *testing.T, command, code, message string) string {
	t.Helper()
	ok := false
	data := mustBobJSON(t, map[string]any{"error": map[string]string{"code": code, "message": message}})
	return string(mustBobJSON(t, bobCLIEnvelope{
		SchemaVersion: bobSchemaVersion, OK: &ok, Command: command, Data: data,
		Warnings: []string{}, NextActions: []string{"review bob.yaml"},
	}))
}

func mustBobJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func containsBobWarning(warnings []string, state string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, "repository state "+state) {
			return true
		}
	}
	return false
}

func containsText(values []string, text string) bool {
	for _, value := range values {
		if strings.Contains(value, text) {
			return true
		}
	}
	return false
}

func TestBobHealthRejectsMalformedVersion(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		exit   int
	}{
		{"wrong command", strings.Replace(bobSuccessJSON(t, "version", json.RawMessage(`{"name":"bob","version":"v0.4.0","commit":"x","date":"x"}`)), `"command":"version"`, `"command":"context"`, 1), 0},
		{"unknown data", bobSuccessJSON(t, "version", json.RawMessage(`{"name":"bob","version":"v0.4.0","commit":"x","date":"x","future":true}`)), 0},
		{"nonzero", bobSuccessJSON(t, "version", json.RawMessage(`{"name":"bob","version":"v0.4.0","commit":"x","date":"x"}`)), 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &recordingBobRunner{stdout: test.stdout, exit: test.exit}
			if err := testBob(r).Health(context.Background()); err == nil {
				t.Fatal("malformed version was accepted")
			}
		})
	}
}

func TestBobFailureRunnerErrorIsUnavailable(t *testing.T) {
	r := &recordingBobRunner{err: errors.New("transport failed")}
	result, _ := testBob(r).Execute(context.Background(), Request{Operation: "path", Input: map[string]any{"workspace": bobFixtureWorkspace, "path": "x.go"}})
	if result.Status != StatusUnavailable {
		t.Fatalf("runner error result = %#v", result)
	}
}
