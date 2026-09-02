package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// callReport calls a tool and decodes any JSON report (envelope-embedding).
func callReport(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &out); err != nil {
		t.Fatalf("%s returned non-JSON: %s", name, textOf(res))
	}
	return out, res.IsError
}

func TestMCPLongRunningToolsRoundTrip(t *testing.T) {
	cs, ws := connectProfile(t, ProfileAgent)
	opened := callEnvelope(t, cs, "cortex_open_task", map[string]any{"goal": "survey the repo", "mode": "survey", "workspace": ws})
	taskID, _ := opened["taskId"].(string)
	if taskID == "" || opened["ok"] != true {
		t.Fatalf("open survey: %+v", opened)
	}
	cov, isErr := callReport(t, cs, "cortex_coverage", map[string]any{"taskId": taskID, "workspace": ws})
	if isErr || cov["coverage"] == nil {
		t.Fatalf("coverage: %+v", cov)
	}
	inv := callEnvelope(t, cs, "cortex_investigate", map[string]any{"taskId": taskID, "question": "what is here", "module": "src", "workspace": ws})
	if inv["ok"] != true {
		t.Fatalf("investigate: %+v", inv)
	}
	if _, isErr := callReport(t, cs, "cortex_finding", map[string]any{"taskId": taskID, "operation": "add", "workspace": ws}); !isErr {
		t.Fatal("finding without a title must be an MCP error")
	}
	finding, isErr := callReport(t, cs, "cortex_finding", map[string]any{"taskId": taskID, "operation": "add", "title": "callback ignores errors", "kind": "bug", "workspace": ws})
	if isErr || finding["finding"] == nil {
		t.Fatalf("finding add: %+v", finding)
	}
	fid := finding["finding"].(map[string]any)["id"].(string)
	list, _ := callReport(t, cs, "cortex_finding", map[string]any{"taskId": taskID, "operation": "list", "workspace": ws})
	if counts := list["counts"].(map[string]any); counts["open"].(float64) != 1 {
		t.Fatalf("finding list counts: %+v", counts)
	}
	if _, isErr := callReport(t, cs, "cortex_finding", map[string]any{"taskId": taskID, "operation": "dismiss", "findingId": fid, "workspace": ws}); !isErr {
		t.Fatal("dismiss without a reason must be an MCP error")
	}
	triaged, isErr := callReport(t, cs, "cortex_finding", map[string]any{"taskId": taskID, "operation": "triage", "findingId": fid, "reason": "confirmed", "workspace": ws})
	if isErr || triaged["finding"].(map[string]any)["status"] != "triaged" {
		t.Fatalf("triage: %+v", triaged)
	}
	converted, isErr := callReport(t, cs, "cortex_finding", map[string]any{"taskId": taskID, "operation": "convert", "findingId": fid, "actor": "agent-a", "workspace": ws})
	if isErr || converted["childTaskId"] == "" {
		t.Fatalf("convert: %+v", converted)
	}
	if res, _ := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_finding", Arguments: map[string]any{"taskId": taskID, "operation": "explode", "workspace": ws}}); !res.IsError || !strings.Contains(textOf(res), "operation must be") {
		t.Fatalf("unknown operation: %s", textOf(res))
	}

	dossier, isErr := callReport(t, cs, "cortex_dossier", map[string]any{"operation": "add", "taskId": taskID, "module": "src", "kind": "invariant", "title": "t", "summary": "s", "workspace": ws})
	if isErr || dossier["entry"] == nil {
		t.Fatalf("dossier add: %+v", dossier)
	}
	listed, _ := callReport(t, cs, "cortex_dossier", map[string]any{"operation": "list", "workspace": ws})
	if listed["total"].(float64) != 1 {
		t.Fatalf("dossier list: %+v", listed)
	}
	refreshed, isErr := callReport(t, cs, "cortex_dossier", map[string]any{"operation": "refresh", "workspace": ws})
	if isErr || refreshed["ok"] != true {
		t.Fatalf("dossier refresh: %+v", refreshed)
	}
	if res, _ := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_dossier", Arguments: map[string]any{"operation": "burn", "workspace": ws}}); !res.IsError {
		t.Fatal("unknown dossier operation must be an error")
	}

	plan, isErr := callReport(t, cs, "cortex_workplan", map[string]any{"taskId": taskID, "operation": "add", "itemId": "a", "goal": "do a", "mode": "change", "workspace": ws})
	if isErr || plan["ready"].(float64) != 1 {
		t.Fatalf("workplan add: %+v", plan)
	}
	listedPlan, _ := callReport(t, cs, "cortex_workplan", map[string]any{"taskId": taskID, "operation": "list", "workspace": ws})
	if len(listedPlan["items"].([]any)) != 1 {
		t.Fatalf("workplan list: %+v", listedPlan)
	}
	next, isErr := callReport(t, cs, "cortex_workplan", map[string]any{"taskId": taskID, "operation": "next", "actor": "agent-b", "workspace": ws})
	if isErr || next["childTaskId"] == "" {
		t.Fatalf("workplan next: %+v", next)
	}
	if res, _ := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_workplan", Arguments: map[string]any{"taskId": taskID, "operation": "zap", "workspace": ws}}); !res.IsError {
		t.Fatal("unknown workplan operation must be an error")
	}

	jobs, isErr := callReport(t, cs, "cortex_job", map[string]any{"taskId": taskID, "operation": "list", "workspace": ws})
	if isErr || jobs["running"].(float64) != 0 {
		t.Fatalf("job list: %+v", jobs)
	}
	if _, isErr := callReport(t, cs, "cortex_job", map[string]any{"taskId": taskID, "operation": "cancel", "jobId": "job_nope", "workspace": ws}); !isErr {
		t.Fatal("cancel of an unknown job must be an error")
	}
	if res, _ := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_job", Arguments: map[string]any{"taskId": taskID, "operation": "zap", "workspace": ws}}); !res.IsError {
		t.Fatal("unknown job operation must be an error")
	}

	resume, isErr := callReport(t, cs, "cortex_resume", map[string]any{"taskId": taskID, "workspace": ws})
	if isErr || !strings.Contains(resume["checkpoint"].(string), "Cortex compact handoff") {
		t.Fatalf("resume: %+v", resume)
	}
	if res, _ := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_resume", Arguments: map[string]any{"taskId": taskID, "since": "yesterday", "workspace": ws}}); !res.IsError || !strings.Contains(textOf(res), "RFC3339") {
		t.Fatalf("bad cursor: %s", textOf(res))
	}
	status := callEnvelope(t, cs, "cortex_status", map[string]any{"taskId": taskID, "workspace": ws})
	if status["coverage"] == nil || status["findings"] == nil || status["workplan"] == nil {
		t.Fatalf("status should carry long-running rollups: %+v", status)
	}
}
