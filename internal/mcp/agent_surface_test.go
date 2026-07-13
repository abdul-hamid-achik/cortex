package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var qaAgentTools = []string{
	"cortex_start_task", "cortex_open_task", "cortex_investigate", "cortex_plan",
	"cortex_begin_change", "cortex_verify", "cortex_remember", "cortex_status",
	"cortex_resolve", "cortex_note", "cortex_request_decision", "cortex_answer_decision",
	"cortex_handoff", "cortex_abort_task", "cortex_read_evidence", "cortex_read_artifact",
	"cortex_recall_cases",
}

var qaOperatorTools = []string{
	"cortex_list_tasks", "cortex_sessions", "cortex_timeline", "cortex_metrics",
	"cortex_overview", "cortex_archive", "cortex_unarchive",
}

func TestMCPDefaultAndAllProfilesExposeExactToolSets(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		want    []string
	}{
		{name: "default", profile: "", want: qaAgentTools},
		{name: "agent", profile: ProfileAgent, want: qaAgentTools},
		{name: "all", profile: ProfileAll, want: append(append([]string(nil), qaAgentTools...), qaOperatorTools...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, _ := connectProfile(t, tt.profile)
			tools := qaListTools(t, cs)
			got := make([]string, 0, len(tools))
			for name := range tools {
				got = append(got, name)
			}
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("%s profile tools =\n%s\nwant\n%s", tt.name, strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

func TestMCPAgentInstructionsDescribeExposedLeaseWorkflow(t *testing.T) {
	cs, _ := connectProfile(t, "")
	init := cs.InitializeResult()
	if init == nil {
		t.Fatal("missing initialize result")
	}
	for _, want := range []string{"cortex_open_task", "idempotencyKey", "acceptanceCriteria", "cortex_begin_change", "lease actor", "claimSpecs", "cortex_request_decision", "cortex_read_artifact"} {
		if !strings.Contains(init.Instructions, want) {
			t.Errorf("agent instructions missing %q:\n%s", want, init.Instructions)
		}
	}
	if strings.Contains(init.Instructions, "cortex_list_tasks") {
		t.Errorf("default agent instructions advertise a hidden operator tool:\n%s", init.Instructions)
	}
}

func TestMCPAgentSurfaceSchemas(t *testing.T) {
	cs, _ := connectProfile(t, ProfileAgent)
	tools := qaListTools(t, cs)
	tests := []struct {
		name       string
		required   []string
		properties []string
	}{
		{name: "cortex_open_task", required: []string{"goal"}, properties: []string{"acceptanceCriteria", "actor", "idempotencyKey", "parentTaskId"}},
		{name: "cortex_plan", required: []string{"hypotheses", "taskId", "uncertainty"}, properties: []string{"files", "symbols", "verification"}},
		{name: "cortex_begin_change", required: []string{"actor", "taskId"}, properties: []string{"ttl", "workspace"}},
		{name: "cortex_verify", required: []string{"taskId"}, properties: []string{"actor", "claimSpecs", "noOpAcknowledged"}},
		{name: "cortex_note", required: []string{"claim", "taskId"}, properties: []string{"category", "origin", "uri"}},
		{name: "cortex_request_decision", required: []string{"options", "question", "requester", "taskId"}, properties: []string{"workspace"}},
		{name: "cortex_answer_decision", required: []string{"taskId"}, properties: []string{"answer", "decisionId", "responder", "resume"}},
		{name: "cortex_handoff", required: []string{"taskId"}, properties: []string{"workspace"}},
		{name: "cortex_read_artifact", required: []string{"ref", "taskId"}, properties: []string{"allowBinary", "maxBytes", "path", "workspace"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tools[tt.name]
			if tool == nil {
				t.Fatalf("tool %s is not exposed", tt.name)
			}
			schema := qaSchemaMap(t, tool.InputSchema)
			gotRequired := qaStringSlice(t, schema["required"])
			sort.Strings(gotRequired)
			wantRequired := append([]string(nil), tt.required...)
			sort.Strings(wantRequired)
			if strings.Join(gotRequired, ",") != strings.Join(wantRequired, ",") {
				t.Errorf("required = %v, want %v", gotRequired, wantRequired)
			}
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("properties has type %T in schema %#v", schema["properties"], schema)
			}
			for _, property := range append(tt.required, tt.properties...) {
				if _, ok := properties[property]; !ok {
					t.Errorf("schema is missing property %q: %#v", property, properties)
				}
			}
		})
	}

	// Nested typed claims and hypotheses must enforce the fields that make them
	// actionable instead of allowing an impossible object through to a handler.
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{tool: "cortex_verify", args: map[string]any{
			"taskId": "task_missing", "claimSpecs": []any{map[string]any{"statement": "redirect works", "contract": "codemap_review"}},
		}, want: "surface"},
		{tool: "cortex_verify", args: map[string]any{
			"taskId": "task_missing", "claimSpecs": []any{map[string]any{"statement": "redirect works", "surface": "code"}},
		}, want: "contract"},
		{tool: "cortex_plan", args: map[string]any{
			"taskId": "task_missing", "uncertainty": "unknown",
			"hypotheses": []any{map[string]any{"statement": "redirect is dropped"}},
		}, want: "disproveBy"},
		{tool: "cortex_open_task", args: map[string]any{
			"goal": "invalid criterion", "acceptanceCriteria": []any{map[string]any{"id": "redirect_works"}},
		}, want: "statement"},
	} {
		res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Fatalf("call %s: %v", tc.tool, err)
		}
		if !res.IsError || !strings.Contains(textOf(res), tc.want) {
			t.Errorf("%s invalid nested input should fail schema validation for %q: error=%t text=%s", tc.tool, tc.want, res.IsError, textOf(res))
		}
	}
}

func TestMCPOpenAcceptanceCriteriaRemainOptionalAndReachStatusProofs(t *testing.T) {
	cs, ws := connectProfile(t, ProfileAgent)
	env := callEnvelope(t, cs, "cortex_open_task", map[string]any{
		"goal": "prove the redirect", "workspace": ws, "idempotencyKey": "mcp-criteria",
		"acceptanceCriteria": []any{map[string]any{
			"id": "redirect_works", "statement": "Redirect preserves the return path",
		}},
	})
	taskID, _ := env["taskId"].(string)
	if taskID == "" {
		t.Fatalf("open did not return task id: %v", env)
	}
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_status", Arguments: map[string]any{
		"taskId": taskID, "workspace": ws,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &status); err != nil {
		t.Fatalf("status is not JSON: %v (%s)", err, textOf(res))
	}
	proofs, _ := status["claimProofs"].([]any)
	if len(proofs) != 1 {
		t.Fatalf("claim proofs = %#v", status["claimProofs"])
	}
	proof, _ := proofs[0].(map[string]any)
	if proof["claimId"] != "redirect_works" || proof["status"] != "not_run" {
		t.Fatalf("claim proof = %#v", proof)
	}
}

func TestMCPSessionsQueryUsesSharedANDSearch(t *testing.T) {
	cs, ws := connectProfile(t, ProfileAll)
	selected := callEnvelope(t, cs, "cortex_open_task", map[string]any{
		"goal": "repair billing redirect", "workspace": ws, "idempotencyKey": "sessions-query-selected",
	})
	_ = callEnvelope(t, cs, "cortex_open_task", map[string]any{
		"goal": "refresh documentation", "workspace": ws, "idempotencyKey": "sessions-query-other",
	})
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_sessions", Arguments: map[string]any{
		"query": "BILLING investigating",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var sessions []map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &sessions); err != nil {
		t.Fatalf("sessions is not JSON: %v (%s)", err, textOf(res))
	}
	if len(sessions) != 1 || sessions[0]["id"] != selected["taskId"] {
		t.Fatalf("query returned %#v, selected=%v", sessions, selected["taskId"])
	}
}

func TestMCPToolsAdvertisePortableMetadataAndEnvelopeSchemas(t *testing.T) {
	cs, _ := connectProfile(t, ProfileAll)
	tools := qaListTools(t, cs)
	readOnly := map[string]bool{
		"cortex_status": true, "cortex_list_tasks": true, "cortex_sessions": true,
		"cortex_timeline": true, "cortex_metrics": true, "cortex_overview": true,
		"cortex_handoff": true, "cortex_read_evidence": true,
		"cortex_read_artifact": true, "cortex_recall_cases": true,
	}
	openWorld := map[string]bool{
		"cortex_start_task": true, "cortex_open_task": true, "cortex_investigate": true,
		"cortex_verify": true, "cortex_remember": true, "cortex_resolve": true,
		"cortex_recall_cases": true,
	}
	destructive := map[string]bool{
		"cortex_plan": true, "cortex_begin_change": true, "cortex_verify": true,
		"cortex_remember": true, "cortex_archive": true, "cortex_unarchive": true,
		"cortex_resolve": true, "cortex_request_decision": true,
		"cortex_answer_decision": true, "cortex_abort_task": true,
	}
	for name, tool := range tools {
		if tool.Title == "" || tool.Annotations == nil || tool.Annotations.Title != tool.Title {
			t.Errorf("%s has no portable display metadata: title=%q annotations=%+v", name, tool.Title, tool.Annotations)
			continue
		}
		if tool.Annotations.ReadOnlyHint != readOnly[name] {
			t.Errorf("%s readOnlyHint=%t, want %t", name, tool.Annotations.ReadOnlyHint, readOnly[name])
		}
		if tool.Annotations.DestructiveHint == nil || tool.Annotations.OpenWorldHint == nil {
			t.Errorf("%s must state explicit destructive/open-world hints: %+v", name, tool.Annotations)
		} else {
			if *tool.Annotations.OpenWorldHint != openWorld[name] {
				t.Errorf("%s openWorldHint=%t, want %t", name, *tool.Annotations.OpenWorldHint, openWorld[name])
			}
			if *tool.Annotations.DestructiveHint != destructive[name] {
				t.Errorf("%s destructiveHint=%t, want %t", name, *tool.Annotations.DestructiveHint, destructive[name])
			}
		}
	}

	if tools["cortex_begin_change"].Annotations.IdempotentHint {
		t.Error("begin-change renews its lease timestamp and must not claim unconditional idempotency")
	}
	for _, name := range []string{
		"cortex_open_task", "cortex_remember", "cortex_archive", "cortex_unarchive", "cortex_resolve",
		"cortex_request_decision", "cortex_answer_decision", "cortex_abort_task",
	} {
		if !tools[name].Annotations.IdempotentHint {
			t.Errorf("%s is retry-safe and should advertise idempotency", name)
		}
	}
	for _, name := range []string{
		"cortex_start_task", "cortex_open_task", "cortex_investigate", "cortex_plan",
		"cortex_begin_change", "cortex_verify", "cortex_remember", "cortex_resolve",
		"cortex_note", "cortex_request_decision", "cortex_answer_decision",
		"cortex_abort_task", "cortex_recall_cases",
	} {
		tool := tools[name]
		if tool.OutputSchema == nil {
			t.Errorf("%s omits the shared envelope output schema", name)
			continue
		}
		schema := qaSchemaMap(t, tool.OutputSchema)
		required := qaStringSlice(t, schema["required"])
		for _, field := range []string{"ok", "summary", "rawAvailable"} {
			found := false
			for _, candidate := range required {
				if candidate == field {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s output schema does not require %q: %v", name, field, required)
			}
		}
	}
}

func TestMCPEnvelopeStructuredContentMatchesTextOnSuccessAndRejection(t *testing.T) {
	cs, ws := connectProfile(t, ProfileAgent)
	tests := []struct {
		name string
		tool string
		args map[string]any
		err  bool
	}{
		{name: "success", tool: "cortex_open_task", args: map[string]any{
			"goal": "portable structured output", "workspace": ws, "idempotencyKey": "structured-output",
		}},
		{name: "kernel rejection", tool: "cortex_plan", args: map[string]any{
			"taskId": "task_missing", "workspace": ws, "uncertainty": "unknown",
			"files": []any{"callback.go"}, "hypotheses": []any{map[string]any{
				"statement": "callback is wrong", "disproveBy": "inspect callback",
			}},
		}, err: true},
		{name: "pre-kernel ttl rejection", tool: "cortex_begin_change", args: map[string]any{
			"taskId": "task_missing", "workspace": ws, "actor": "agent-a", "ttl": "not-a-duration",
		}, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError != tt.err {
				t.Fatalf("isError=%t, want %t: %s", res.IsError, tt.err, textOf(res))
			}
			qaRequireEnvelopeParity(t, res)
		})
	}
}

func TestMCPSharedEnvelopeToolsStructureKernelConstructionErrors(t *testing.T) {
	cs, _ := connectProfile(t, ProfileAgent)
	badWorkspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(badWorkspace, "cortex.yaml"), []byte("budget:\n  max_parallel_calls: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		tool string
		args map[string]any
	}{
		{tool: "cortex_start_task", args: map[string]any{"goal": "start"}},
		{tool: "cortex_open_task", args: map[string]any{"goal": "open"}},
		{tool: "cortex_investigate", args: map[string]any{"taskId": "task_missing", "question": "why"}},
		{tool: "cortex_plan", args: map[string]any{
			"taskId": "task_missing", "uncertainty": "unknown", "files": []any{"callback.go"},
			"hypotheses": []any{map[string]any{"statement": "broken", "disproveBy": "inspect"}},
		}},
		{tool: "cortex_begin_change", args: map[string]any{"taskId": "task_missing", "actor": "agent-a"}},
		{tool: "cortex_verify", args: map[string]any{"taskId": "task_missing"}},
		{tool: "cortex_remember", args: map[string]any{"taskId": "task_missing", "outcome": "done"}},
		{tool: "cortex_resolve", args: map[string]any{
			"taskId": "task_missing", "hypothesisId": "hyp_missing", "status": "rejected", "reason": "disproved",
		}},
		{tool: "cortex_note", args: map[string]any{"taskId": "task_missing", "claim": "note"}},
		{tool: "cortex_request_decision", args: map[string]any{
			"taskId": "task_missing", "question": "choose", "requester": "agent-a",
			"options": []any{
				map[string]any{"id": "a", "label": "A", "consequence": "first"},
				map[string]any{"id": "b", "label": "B", "consequence": "second"},
			},
		}},
		{tool: "cortex_answer_decision", args: map[string]any{"taskId": "task_missing"}},
		{tool: "cortex_abort_task", args: map[string]any{"taskId": "task_missing", "reason": "stop"}},
		{tool: "cortex_recall_cases", args: map[string]any{"query": "prior disproof"}},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			tt.args["workspace"] = badWorkspace
			res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError {
				t.Fatalf("kernel construction failure was reported as success: %s", textOf(res))
			}
			env := qaRequireEnvelopeParity(t, res)
			if env["ok"] != false || !strings.Contains(env["error"].(string), "invalid cortex configuration") {
				t.Fatalf("kernel construction error lost its envelope: %v", env)
			}
		})
	}
}

func qaRequireEnvelopeParity(t *testing.T, res *sdkmcp.CallToolResult) map[string]any {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatal("structuredContent is missing")
	}
	var textValue, structuredValue map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &textValue); err != nil {
		t.Fatalf("decode text result: %v", err)
	}
	structuredJSON, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("encode structured result: %v", err)
	}
	if err := json.Unmarshal(structuredJSON, &structuredValue); err != nil {
		t.Fatalf("decode structured result: %v (%s)", err, structuredJSON)
	}
	if !reflect.DeepEqual(textValue, structuredValue) {
		t.Fatalf("text and structured results differ:\ntext=%v\nstructured=%v", textValue, structuredValue)
	}
	return textValue
}

func TestMCPOperatorTimelineSchemaAcceptsWorkspaceFallback(t *testing.T) {
	cs, _ := connectProfile(t, ProfileAll)
	tool := qaListTools(t, cs)["cortex_timeline"]
	if tool == nil {
		t.Fatal("cortex_timeline is not exposed by the all profile")
	}
	schema := qaSchemaMap(t, tool.InputSchema)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("timeline properties have type %T", schema["properties"])
	}
	if _, ok := properties["workspace"]; !ok {
		t.Fatalf("timeline schema omits repo-local workspace fallback: %#v", properties)
	}
}

func TestMCPAgentWorkflowPreservesActorsTypedClaimsAndActions(t *testing.T) {
	cs, ws := connectProfile(t, "")
	openArgs := map[string]any{
		"goal": "repair redirect contract", "workspace": ws, "actor": "agent-a", "idempotencyKey": "qa-agent-run",
	}
	opened := callEnvelope(t, cs, "cortex_open_task", openArgs)
	retried := callEnvelope(t, cs, "cortex_open_task", openArgs)
	if opened["taskId"] == "" || retried["taskId"] != opened["taskId"] {
		t.Fatalf("open retry changed identity: first=%v retry=%v", opened, retried)
	}
	taskID := opened["taskId"].(string)
	qaRequireAction(t, opened, "cortex_investigate")

	note := callEnvelope(t, cs, "cortex_note", map[string]any{
		"taskId": taskID, "workspace": ws, "claim": "redirect behavior is externally visible",
		"category": "constraint", "origin": "agent", "actor": "agent-a",
	})
	if note["ok"] != true {
		t.Fatalf("note = %v", note)
	}
	qaRequireAction(t, note, "cortex_investigate")

	paused := callEnvelope(t, cs, "cortex_request_decision", map[string]any{
		"taskId": taskID, "workspace": ws, "question": "Which rollout should we use?", "requester": "agent-a",
		"options": []any{
			map[string]any{"id": "safe", "label": "Two-step", "consequence": "Slower but reversible"},
			map[string]any{"id": "fast", "label": "One-step", "consequence": "Faster but harder rollback"},
		},
	})
	decisionID := paused["artifacts"].([]any)[0].(map[string]any)["id"].(string)
	decisionAction := qaRequireAction(t, paused, "cortex_answer_decision")
	if decisionAction["arguments"].(map[string]any)["decisionId"] != decisionID || !strings.Contains(decisionAction["command"].(string), decisionID) {
		t.Fatalf("decision action is not directly invokable: %v", decisionAction)
	}
	answered := callEnvelope(t, cs, "cortex_answer_decision", map[string]any{
		"taskId": taskID, "workspace": ws, "decisionId": decisionID, "answer": "safe", "responder": "human-a",
	})
	if answered["phase"] != "investigating" {
		t.Fatalf("decision did not resume investigating: %v", answered)
	}
	qaRequireAction(t, answered, "cortex_investigate")

	planned := callEnvelope(t, cs, "cortex_plan", map[string]any{
		"taskId": taskID, "workspace": ws, "uncertainty": "redirect signing may differ",
		"files": []any{"callback.go"},
		"hypotheses": []any{map[string]any{
			"statement": "callback drops the return path", "disproveBy": "review the callback diff",
		}},
	})
	if planned["phase"] != "planned" {
		t.Fatalf("plan = %v", planned)
	}
	planAction := qaRequireAction(t, planned, "cortex_begin_change")
	if !qaAnyStringContains(planAction["inputs"], "actor") {
		t.Fatalf("begin-change action omits its required actor input: %v", planAction)
	}

	begun := callEnvelope(t, cs, "cortex_begin_change", map[string]any{
		"taskId": taskID, "workspace": ws, "actor": "agent-a", "ttl": "2m",
	})
	if begun["phase"] != "changing" {
		t.Fatalf("begin-change = %v", begun)
	}
	verifyAction := qaRequireAction(t, begun, "cortex_verify")
	if verifyAction["arguments"].(map[string]any)["actor"] != "agent-a" || !strings.Contains(verifyAction["command"].(string), "--actor agent-a") {
		t.Fatalf("leased verify action is not bound to its actor: %v", verifyAction)
	}
	if err := os.WriteFile(filepath.Join(ws, "callback.go"), []byte("package a\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withoutActorResult, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_verify", Arguments: map[string]any{
		"taskId": taskID, "workspace": ws,
		"claimSpecs": []any{map[string]any{
			"id": "claim_redirect", "statement": "redirect is preserved", "surface": "code", "contract": "codemap_review",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !withoutActorResult.IsError {
		t.Fatalf("leased verify rejection must set MCP isError: %s", textOf(withoutActorResult))
	}
	var withoutActor map[string]any
	if err := json.Unmarshal([]byte(textOf(withoutActorResult)), &withoutActor); err != nil {
		t.Fatalf("leased verify rejection lost its JSON envelope: %v (%s)", err, textOf(withoutActorResult))
	}
	if withoutActor["ok"] != false || !strings.Contains(withoutActor["error"].(string), "verify must name that actor") {
		t.Fatalf("leased verify without actor should fail: %v", withoutActor)
	}

	verified := callEnvelope(t, cs, "cortex_verify", map[string]any{
		"taskId": taskID, "workspace": ws, "actor": "agent-a",
		"claimSpecs": []any{map[string]any{
			"id": "claim_redirect", "statement": "redirect is preserved", "surface": "code",
			"verifier": "codemap", "contract": "codemap_review",
		}},
	})
	if verified["ok"] != true || verified["phase"] != "verifying" {
		t.Fatalf("leased typed verify = %v", verified)
	}
	qaRequireAction(t, verified, "cortex_verify")

	handoff := callEnvelope(t, cs, "cortex_handoff", map[string]any{"taskId": taskID})
	if handoff["taskId"] != taskID || len(handoff["actions"].([]any)) == 0 {
		t.Fatalf("handoff is missing task/actions: %v", handoff)
	}
	if !qaReceiptHasTypedClaim(handoff["receipts"], "claim_redirect", "code", "codemap_review") {
		t.Fatalf("typed claim fields did not survive MCP verify into handoff: %v", handoff["receipts"])
	}
}

func qaListTools(t *testing.T, cs *sdkmcp.ClientSession) map[string]*sdkmcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*sdkmcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func qaSchemaMap(t *testing.T, schema any) map[string]any {
	t.Helper()
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("schema is not an object: %v (%s)", err, b)
	}
	return out
}

func qaStringSlice(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("value has type %T, want array", value)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("array item has type %T, want string", item)
		}
		out = append(out, s)
	}
	return out
}

func qaRequireAction(t *testing.T, env map[string]any, wantTool string) map[string]any {
	t.Helper()
	actions, ok := env["actions"].([]any)
	if !ok || len(actions) == 0 {
		t.Fatalf("%s result has no structured actions: %v", wantTool, env)
	}
	action, ok := actions[0].(map[string]any)
	if !ok || action["tool"] != wantTool {
		t.Fatalf("first action = %v, want tool %s", actions[0], wantTool)
	}
	return action
}

func qaReceiptHasTypedClaim(raw any, claimID, surface, contract string) bool {
	receipts, _ := raw.([]any)
	for _, item := range receipts {
		receipt, _ := item.(map[string]any)
		if receipt["claimId"] == claimID && receipt["surface"] == surface && receipt["contract"] == contract {
			return true
		}
	}
	return false
}

func qaAnyStringContains(raw any, want string) bool {
	items, _ := raw.([]any)
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
