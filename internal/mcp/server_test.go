package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testRepo makes a temp git repo the MCP tools can operate on.
func testRepo(t *testing.T) string {
	t.Helper()
	// Isolate global dirs so tool calls write cases into a throwaway base, not
	// the developer's real $XDG_STATE_HOME/cortex.
	t.Setenv("CORTEX_HOME", t.TempDir())
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t.co"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "callback.go"), []byte("package a\nfunc HandleCallback(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-qm", "init")
	cmd.Dir = dir
	_ = cmd.Run()
	return dir
}

func TestMCPStdioSubprocessHandshakeUsesCleanNewlineFraming(t *testing.T) {
	ws := testRepo(t)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), stdioHelperEnv+"=1", "CORTEX_TEST_MCP_WORKSPACE="+ws)
	var stderr synchronizedBuffer
	cmd.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "stdio-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &sdkmcp.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("stdio handshake: %v (stderr: %s)", err, stderr.String())
	}
	cleanupNeeded := true
	t.Cleanup(func() {
		if cleanupNeeded {
			_ = cs.Close()
		}
	})
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools over stdio: %v (stderr: %s)", err, stderr.String())
	}
	if len(tools.Tools) != len(qaAgentTools) {
		t.Fatalf("stdio tool count=%d, want %d", len(tools.Tools), len(qaAgentTools))
	}
	closed := make(chan error, 1)
	go func() { closed <- cs.Close() }()
	select {
	case err := <-closed:
		cleanupNeeded = false
		if err != nil {
			t.Fatalf("clean stdio shutdown: %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		cleanupNeeded = false
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
		}
		t.Fatal("stdio server did not shut down within 5s after the client closed stdin")
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stdio server wrote unexpected diagnostics: %s", stderr.String())
	}
}

// connect starts the server over an in-memory transport and returns a client
// session plus the workspace all calls should target.
func connect(t *testing.T) (*sdkmcp.ClientSession, string) {
	return connectProfile(t, ProfileAll)
}

func connectProfile(t *testing.T, profile Profile) (*sdkmcp.ClientSession, string) {
	t.Helper()
	ws := testRepo(t)
	ctx := context.Background()
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	srv, err := NewServerWithProfile(ws, string(profile))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.serve(ctx, serverT) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ws
}

func textOf(res *sdkmcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// callEnvelope calls a tool and decodes the shared result envelope.
func callEnvelope(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &env); err != nil {
		t.Fatalf("call %s: result is not JSON: %s", name, textOf(res))
	}
	return env
}

func TestResultMarksAndPreservesStructuredEnvelopeErrors(t *testing.T) {
	tests := []struct {
		name string
		v    any
		err  error
	}{
		{
			name: "kernel rule rejection",
			v: domain.Envelope{
				OK: false, TaskID: "task_rejected", Phase: domain.PhaseChanging,
				Summary: "lease actor is required", Error: "lease actor is required",
			},
		},
		{
			name: "structured envelope plus go error",
			v: domain.Envelope{
				OK: false, TaskID: "task_conflict", Summary: "case changed concurrently",
				Error: "case changed concurrently",
			},
			err: errors.New("case revision conflict"),
		},
		{
			name: "embedded status rejection",
			v: kernel.StatusReport{Envelope: domain.Envelope{
				OK: false, TaskID: "task_missing", Summary: "not found", Error: "not found",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, structured, callErr := result(tt.v, tt.err)
			if callErr != nil {
				t.Fatal(callErr)
			}
			if !res.IsError {
				t.Fatalf("structured rejection was reported as success: %+v", res)
			}
			if structured == nil {
				t.Fatal("structured rejection was discarded")
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(textOf(res)), &decoded); err != nil {
				t.Fatalf("structured rejection is not JSON: %v (%s)", err, textOf(res))
			}
			if decoded["ok"] != false || decoded["taskId"] == "" {
				t.Fatalf("structured rejection lost envelope fields: %v", decoded)
			}
		})
	}

	plain, structured, err := result(nil, errors.New("kernel construction failed"))
	if err != nil || !plain.IsError || structured != nil || !strings.HasPrefix(textOf(plain), "Error: ") {
		t.Fatalf("unstructured errors should retain the plain fallback: result=%+v structured=%v err=%v", plain, structured, err)
	}
}

func TestMCPToolsList(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{
		"cortex_start_task", "cortex_open_task", "cortex_investigate", "cortex_plan", "cortex_begin_change", "cortex_verify",
		"cortex_remember", "cortex_status", "cortex_list_tasks", "cortex_sessions",
		"cortex_timeline", "cortex_metrics", "cortex_overview", "cortex_archive",
		"cortex_unarchive", "cortex_resolve", "cortex_abort_task", "cortex_read_evidence",
		"cortex_read_artifact", "cortex_recall_cases",
		"cortex_note", "cortex_request_decision", "cortex_answer_decision", "cortex_handoff",
	} {
		if !got[want] {
			t.Errorf("missing MCP tool %q", want)
		}
	}
	if len(res.Tools) != 24 {
		t.Errorf("expected 24 tools, got %d", len(res.Tools))
	}
}

func TestMCPAgentProfileHidesOperatorTools(t *testing.T) {
	cs, _ := connectProfile(t, ProfileAgent)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"cortex_start_task", "cortex_open_task", "cortex_begin_change", "cortex_verify", "cortex_resolve", "cortex_read_evidence", "cortex_note", "cortex_request_decision", "cortex_answer_decision", "cortex_handoff"} {
		if !got[want] {
			t.Errorf("agent profile is missing %q", want)
		}
	}
	for _, hidden := range []string{"cortex_sessions", "cortex_metrics", "cortex_archive"} {
		if got[hidden] {
			t.Errorf("agent profile should hide %q", hidden)
		}
	}
}

func TestMCPOpenAndBeginChangeAreRetrySafe(t *testing.T) {
	cs, ws := connect(t)
	args := map[string]any{
		"goal": "idempotent repair", "workspace": ws, "actor": "agent-a", "idempotencyKey": "mcp-run-1",
	}
	first := callEnvelope(t, cs, "cortex_open_task", args)
	retry := callEnvelope(t, cs, "cortex_open_task", args)
	if first["taskId"] == "" || retry["taskId"] != first["taskId"] {
		t.Fatalf("open was not idempotent: first=%v retry=%v", first, retry)
	}
	taskID, _ := first["taskId"].(string)
	plan := callEnvelope(t, cs, "cortex_plan", map[string]any{
		"taskId": taskID, "workspace": ws, "uncertainty": "coverage may differ",
		"files":      []any{"callback.go"},
		"hypotheses": []any{map[string]any{"statement": "callback needs a change", "disproveBy": "review the diff"}},
	})
	if plan["ok"] != true {
		t.Fatalf("plan = %v", plan)
	}
	begun := callEnvelope(t, cs, "cortex_begin_change", map[string]any{
		"taskId": taskID, "workspace": ws, "actor": "agent-a", "ttl": "2m",
	})
	if begun["ok"] != true || begun["phase"] != "changing" {
		t.Fatalf("begin = %v", begun)
	}
	retried := callEnvelope(t, cs, "cortex_begin_change", map[string]any{
		"taskId": taskID, "workspace": ws, "actor": "agent-a", "ttl": "2m",
	})
	if retried["ok"] != true || retried["taskId"] != taskID {
		t.Fatalf("begin retry = %v", retried)
	}
}

func TestMCPDecisionPauseAnswerAndHandoff(t *testing.T) {
	cs, ws := connect(t)
	start := callEnvelope(t, cs, "cortex_start_task", map[string]any{
		"goal": "choose migration strategy", "workspace": ws,
	})
	taskID, _ := start["taskId"].(string)
	paused := callEnvelope(t, cs, "cortex_request_decision", map[string]any{
		"taskId": taskID, "workspace": ws, "question": "Which migration should we use?", "requester": "agent-a",
		"options": []any{
			map[string]any{"id": "safe", "label": "Safe migration", "consequence": "Takes longer"},
			map[string]any{"id": "fast", "label": "Fast migration", "consequence": "Higher rollback risk"},
		},
	})
	if paused["ok"] != true || paused["phase"] != "needs_human_decision" {
		t.Fatalf("pause = %v", paused)
	}
	artifacts, _ := paused["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("decision artifact missing: %v", paused)
	}
	decision, _ := artifacts[0].(map[string]any)
	decisionID, _ := decision["id"].(string)
	answered := callEnvelope(t, cs, "cortex_answer_decision", map[string]any{
		"taskId": taskID, "workspace": ws, "decisionId": decisionID, "answer": "safe", "responder": "human-a",
	})
	if answered["ok"] != true || answered["phase"] != "investigating" {
		t.Fatalf("answer = %v", answered)
	}
	handoff := callEnvelope(t, cs, "cortex_handoff", map[string]any{"taskId": taskID})
	if handoff["taskId"] != taskID || handoff["phase"] != "investigating" {
		t.Fatalf("handoff = %v", handoff)
	}
}

func TestMCPWorkspaceViewsFindRepoLocalCases(t *testing.T) {
	workspace := testRepo(t)
	if err := os.WriteFile(filepath.Join(workspace, "cortex.yaml"), []byte("cases_dir: .cortex/cases\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := kernel.New(config.For(workspace))
	if err != nil {
		t.Fatal(err)
	}
	started, err := k.StartTask(context.Background(), kernel.StartInput{Goal: "portable MCP case", Actor: "agent-a"})
	if err != nil || !started.OK {
		t.Fatalf("start repo-local case: %+v (%v)", started, err)
	}

	ctx := context.Background()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	server, err := NewServerWithProfile(t.TempDir(), string(ProfileAll))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.serve(ctx, serverTransport) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "workspace-view-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	handoff := callEnvelope(t, session, "cortex_handoff", map[string]any{
		"taskId": started.TaskID, "workspace": workspace,
	})
	if handoff["taskId"] != started.TaskID {
		t.Fatalf("workspace-aware handoff = %v", handoff)
	}
	actions, _ := handoff["actions"].([]any)
	if len(actions) == 0 {
		t.Fatalf("handoff omitted actions: %v", handoff)
	}
	action, _ := actions[0].(map[string]any)
	arguments, _ := action["arguments"].(map[string]any)
	if arguments["workspace"] != workspace {
		t.Fatalf("handoff action is not portable: %v", action)
	}

	timelineResult, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cortex_timeline", Arguments: map[string]any{"taskId": started.TaskID, "workspace": workspace},
	})
	if err != nil || timelineResult.IsError {
		t.Fatalf("workspace-aware timeline: %v (%s)", err, textOf(timelineResult))
	}
	var timeline []kernel.TimelineEntry
	if err := json.Unmarshal([]byte(textOf(timelineResult)), &timeline); err != nil || len(timeline) == 0 {
		t.Fatalf("timeline = %+v (%v; %s)", timeline, err, textOf(timelineResult))
	}
}

func TestMCPInvalidProfileRejected(t *testing.T) {
	if _, err := NewServerWithProfile(t.TempDir(), "everything"); err == nil {
		t.Fatal("invalid MCP profile must be rejected")
	}
}

func TestMCPFullLifecycle(t *testing.T) {
	cs, ws := connect(t)

	start := callEnvelope(t, cs, "cortex_start_task", map[string]any{
		"goal": "fix redirect", "workspace": ws, "surfaces": []any{"code"},
	})
	if start["ok"] != true || start["phase"] != "investigating" {
		t.Fatalf("start: %v", start)
	}
	taskID, _ := start["taskId"].(string)
	if taskID == "" {
		t.Fatal("no taskId from start")
	}

	callEnvelope(t, cs, "cortex_investigate", map[string]any{
		"taskId": taskID, "question": "HandleCallback", "workspace": ws,
	})

	// A plan with an empty disproof path must be REJECTED by the kernel gate
	// across the MCP boundary. (An entirely-missing disproveBy is rejected one
	// layer earlier by the tool's JSON schema — defense in depth.)
	badPlan := callEnvelope(t, cs, "cortex_plan", map[string]any{
		"taskId": taskID, "workspace": ws, "uncertainty": "unsure",
		"files":      []any{"callback.go"},
		"hypotheses": []any{map[string]any{"statement": "returnTo dropped", "disproveBy": ""}},
	})
	if badPlan["ok"] == true {
		t.Errorf("plan with an empty disproof path should be rejected over MCP: %v", badPlan)
	}

	goodPlan := callEnvelope(t, cs, "cortex_plan", map[string]any{
		"taskId": taskID, "workspace": ws, "uncertainty": "unsure about signing",
		"files":      []any{"callback.go"},
		"hypotheses": []any{map[string]any{"statement": "returnTo dropped", "disproveBy": "run the browser flow"}},
	})
	if goodPlan["ok"] != true || goodPlan["phase"] != "planned" {
		t.Fatalf("good plan should be accepted: %v", goodPlan)
	}

	// Edit so verify has a diff.
	if err := os.WriteFile(filepath.Join(ws, "callback.go"), []byte("package a\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verify := callEnvelope(t, cs, "cortex_verify", map[string]any{
		"taskId": taskID, "workspace": ws, "claims": []any{"the code is structurally sound"},
	})
	if verify["ok"] != true || verify["phase"] != "verifying" {
		t.Fatalf("verify: %v", verify)
	}

	remember := callEnvelope(t, cs, "cortex_remember", map[string]any{
		"taskId": taskID, "workspace": ws, "outcome": "fixed returnTo", "verificationNotPossible": true,
	})
	if remember["ok"] != true || remember["phase"] != "complete" {
		t.Fatalf("remember should complete the task: %v", remember)
	}

	status := callEnvelope(t, cs, "cortex_status", map[string]any{"taskId": taskID, "workspace": ws})
	if status["phase"] != "complete" {
		t.Errorf("status should report complete, got %v", status["phase"])
	}
}

func TestMCPInvestigateUnknownTask(t *testing.T) {
	cs, ws := connect(t)
	env := callEnvelope(t, cs, "cortex_investigate", map[string]any{
		"taskId": "task_DOESNOTEXIST", "question": "x", "workspace": ws,
	})
	if env["ok"] == true {
		t.Error("investigating an unknown task should not report ok:true")
	}
}

func TestMCPReadArtifactReturnsBoundedPreview(t *testing.T) {
	cs, ws := connect(t)
	start := callEnvelope(t, cs, "cortex_start_task", map[string]any{
		"goal": "preview raw evidence", "workspace": ws,
	})
	taskID, _ := start["taskId"].(string)
	k, err := kernel.New(config.For(ws))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Store().WriteRaw(taskID, "raw_mcp", "abcdefghij"); err != nil {
		t.Fatal(err)
	}
	preview := callEnvelope(t, cs, "cortex_read_artifact", map[string]any{
		"taskId": taskID, "workspace": ws,
		"ref": "case://" + taskID + "/raw/raw_mcp", "maxBytes": 3,
	})
	if preview["content"] != "abc" || preview["truncated"] != true || preview["maxBytes"] != float64(3) {
		t.Fatalf("MCP read-artifact did not return bounded preview metadata: %+v", preview)
	}
}
