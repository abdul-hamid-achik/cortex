package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// connect starts the server over an in-memory transport and returns a client
// session plus the workspace all calls should target.
func connect(t *testing.T) (*sdkmcp.ClientSession, string) {
	t.Helper()
	ws := testRepo(t)
	ctx := context.Background()
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	srv := NewServer(ws)
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
		"cortex_start_task", "cortex_investigate", "cortex_plan", "cortex_verify",
		"cortex_remember", "cortex_status", "cortex_list_tasks", "cortex_sessions",
		"cortex_timeline", "cortex_metrics", "cortex_overview", "cortex_archive",
		"cortex_unarchive", "cortex_resolve", "cortex_abort_task", "cortex_read_evidence",
		"cortex_read_artifact",
	} {
		if !got[want] {
			t.Errorf("missing MCP tool %q", want)
		}
	}
	if len(res.Tools) != 17 {
		t.Errorf("expected 17 tools, got %d", len(res.Tools))
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
