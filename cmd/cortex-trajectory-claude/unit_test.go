//go:build unix

/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

func sha(data string) string {
	sum := sha256.Sum256([]byte(data))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestRequestDigestHashesRawBytes(t *testing.T) {
	// Non-canonical spacing and key order: a re-marshal would hash differently.
	payload := `{ "workspace" : "/w",  "schemaVersion":1 }`
	if got := requestDigest([]byte(payload + "\n")); got != sha(payload) {
		t.Fatalf("digest with trailing newline = %s, want hash of the payload", got)
	}
	if got := requestDigest([]byte(payload)); got != sha(payload) {
		t.Fatalf("digest without trailing newline = %s", got)
	}
	// Exactly one newline is removed.
	if got := requestDigest([]byte(payload + "\n\n")); got != sha(payload+"\n") {
		t.Fatalf("digest with two trailing newlines = %s, want hash of payload plus one newline", got)
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(payload), &generic); err != nil {
		t.Fatal(err)
	}
	remarshalled, _ := json.Marshal(generic)
	if requestDigest([]byte(payload)) == sha(string(remarshalled)) {
		t.Fatal("test payload should differ from its re-marshalled form")
	}
}

func TestDigestOfNonCanonicalRequestIsEchoed(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	stdin, _ := encode(t, r.request(trajectory.ArmRawTools))
	// Re-indent the same JSON: still valid, but the raw bytes (and digest) differ.
	var generic map[string]any
	if err := json.Unmarshal(stdin, &generic); err != nil {
		t.Fatal(err)
	}
	indented, _ := json.MarshalIndent(generic, "", "  ")
	raw := append(indented, '\n')
	got := r.launchRaw(raw, sha(string(indented)), nil, r.environ())
	if got.result.Status != trajectory.RunCompleted {
		t.Fatalf("status %s: %s", got.result.Status, got.stderr)
	}
}

func TestDecodeRequestIsStrict(t *testing.T) {
	r := newRig(t)
	stdin, _ := encode(t, r.request(trajectory.ArmRawTools))
	if _, err := decodeRequest(stdin); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	withUnknown := strings.Replace(string(stdin), `"goal"`, `"surprise":1,"goal"`, 1)
	if _, err := decodeRequest([]byte(withUnknown)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := decodeRequest([]byte(strings.TrimSpace(string(stdin)) + ` {}`)); err == nil {
		t.Fatal("trailing data must be rejected")
	}
	if _, err := decodeRequest([]byte(`{"schemaVersion":"1"}`)); err == nil {
		t.Fatal("wrong field types must be rejected")
	}
}

func TestParseCompletion(t *testing.T) {
	cases := map[string]baseeval.CompletionLabel{
		"done\nCOMPLETION: verified":          baseeval.CompletionVerified,
		"done\nCOMPLETION:unverified  \n\n":   baseeval.CompletionUnverified,
		"COMPLETION: failed":                  baseeval.CompletionFailed,
		"x\nCOMPLETION: incomplete\r\n":       baseeval.CompletionIncomplete,
		"COMPLETION: verified\nand then more": baseeval.CompletionIncomplete,
		"COMPLETION: maybe":                   baseeval.CompletionIncomplete,
		"**COMPLETION: verified**":            baseeval.CompletionIncomplete,
		"":                                    baseeval.CompletionIncomplete,
	}
	for text, want := range cases {
		if got := parseCompletion(text); got != want {
			t.Errorf("parseCompletion(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestStreamParserCountsDistinctToolUsesAndCaps(t *testing.T) {
	capped := 0
	parser := newStreamParser(2, func() { capped++ })
	lines := []string{
		initLine(t, "m"), toolLine(t, "a"), toolLine(t, "a"), toolLine(t, "b"),
		"not json", toolLine(t, "c"), toolLine(t, "d"),
	}
	for _, line := range lines {
		// Split across writes to exercise line reassembly.
		half := len(line) / 2
		_, _ = parser.Write([]byte(line[:half]))
		_, _ = parser.Write([]byte(line[half:] + "\n"))
	}
	state := parser.snapshot()
	if state.ToolCalls != 2 || !state.Capped || capped != 1 || state.Malformed != 1 || !state.SawInit || state.InitModel != "m" {
		t.Fatalf("state = %+v capped callbacks %d", state, capped)
	}
}

func TestStreamParserResultAndFlush(t *testing.T) {
	parser := newStreamParser(10, nil)
	_, _ = parser.Write([]byte(resultLine(t, "ok\nCOMPLETION: verified", 0.0000004))) // no trailing newline
	if parser.snapshot().Result != nil {
		t.Fatal("an unterminated line must wait for Flush")
	}
	parser.Flush()
	result := parser.snapshot().Result
	if result == nil || result.CostMicros == nil || *result.CostMicros != 0 || *result.InputTokens != 150 {
		t.Fatalf("result = %+v", result)
	}
}

func TestStreamParserDropsOversizedLines(t *testing.T) {
	parser := newStreamParser(10, nil)
	big := strings.Repeat("x", maxStreamLine+10)
	_, _ = parser.Write([]byte(`{"type":"assistant","pad":"` + big + `"}` + "\n"))
	_, _ = parser.Write([]byte(toolLine(t, "a") + "\n"))
	state := parser.snapshot()
	if state.Oversized != 1 || state.ToolCalls != 1 {
		t.Fatalf("state = %+v", state)
	}
}

func writeCase(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"case.json": `{"id":"task_one","changeBoundary":{"files":["greet.go"]}}`,
		"evidence.jsonl": strings.Join([]string{
			`{"id":"ev_1","kind":"code_location","source":{"tool":"codemap","uri":"codemap://x"},"claim":"a"}`,
			`{"id":"ev_2","kind":"code_location","source":{"tool":"vecgrep"},"location":{"file":"greet.go"},"claim":"b"}`,
			`{"id":"ev_3","kind":"human_report","source":{"tool":"human"},"claim":"c"}`,
			`garbage line`,
		}, "\n") + "\n",
		"hypotheses.json": `[
			{"id":"h1","statement":"s","disproveBy":{"note":"run the test"},"status":"confirmed","supports":["ev_1"]},
			{"id":"h2","statement":"s","disproveBy":{"tool":"cairntrace"},"status":"rejected"},
			{"id":"h3","statement":"s","disproveBy":{},"status":"active"}
		]`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObserveCasesToleratesAbsenceAndCorruption(t *testing.T) {
	if got := observeCases(filepath.Join(t.TempDir(), "missing")); got.Evidence.Items != 0 || got.BoundaryDeclared {
		t.Fatalf("missing store observation = %+v", got)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "task")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"case.json", "hypotheses.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := observeCases(root)
	if got.BoundaryDeclared || got.Disproof.Hypotheses != 0 {
		t.Fatalf("corrupt store observation = %+v", got)
	}
	// The observation must satisfy the runner's value <= total ranges.
	writeCase(t, filepath.Join(root, "task2"))
	got = observeCases(root)
	if got.Evidence.Sourced > got.Evidence.Items || got.Disproof.WithDisproofPath > got.Disproof.Hypotheses ||
		got.Disproof.EvidenceGroundedResolutions > got.Disproof.Resolutions {
		t.Fatalf("observation violates ranges: %+v", got)
	}
}

func TestToolchainDigestsTheResolvedFile(t *testing.T) {
	r := newRig(t)
	link := filepath.Join(r.root, "link-to-tool")
	if err := os.Symlink(r.tool, link); err != nil {
		t.Fatal(err)
	}
	provenance, err := collectToolchain(t.Context(), []toolSpec{{Name: "git", Path: link}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(r.tool)
	if provenance[0].BinaryDigest != sha(string(data)) || provenance[0].ExecutablePath != link || provenance[0].Version != "git version 1.0" {
		t.Fatalf("provenance = %+v", provenance[0])
	}
	failing := r.script("fail-bin", "exit 4")
	provenance, err = collectToolchain(t.Context(), []toolSpec{{Name: "bad", Path: failing}})
	if err != nil || provenance[0].Version != "unknown" {
		t.Fatalf("a failing --version must fall back to unknown: %+v, %v", provenance, err)
	}
	if _, err := collectToolchain(t.Context(), []toolSpec{{Name: "gone", Path: filepath.Join(r.root, "gone")}}); err == nil {
		t.Fatal("a missing executable must be an error")
	}
}

func TestParseOptionsValidation(t *testing.T) {
	var sink strings.Builder
	opt, err := parseOptions([]string{"--agent", "/a/claude", "--tool", "go=/a/go", "--tool", "git=/a/git"}, &sink)
	if err != nil || !opt.RequireAPIKey || opt.WallMargin != defaultWallMargin || len(opt.Tools) != 2 {
		t.Fatalf("opt = %+v, err = %v", opt, err)
	}
	for _, args := range [][]string{
		{"--agent", "/a/claude", "--tool", "go=/a/go", "--tool", "go=/b/go"},
		{"--agent", "/a/../a/claude"},
		{"--agent", "/a/claude", "--tool", "Bad Name=/a/x"},
		{"--agent", "/a/claude", "extra"},
		{"--agent", "/a/claude", "--wall-margin", "-1s"},
	} {
		if _, err := parseOptions(args, &sink); err == nil {
			t.Errorf("parseOptions(%v) should fail", args)
		}
	}
}

func TestStreamRecordsMCPStatusAndToolNames(t *testing.T) {
	p := newStreamParser(10, nil)
	_, _ = p.Write([]byte(`{"type":"system","subtype":"init","model":"m","mcp_servers":[{"name":"cortex","status":"connected"}]}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Bash"},{"type":"tool_use","id":"b","name":"mcp__cortex__cortex_open_task"}]}}` + "\n"))
	state := p.snapshot()
	if len(state.MCPServers) != 1 || state.MCPServers[0].Status != "connected" {
		t.Fatalf("mcp servers = %+v", state.MCPServers)
	}
	if state.ToolNames["Bash"] != 1 || state.ToolNames["mcp__cortex__cortex_open_task"] != 1 {
		t.Fatalf("tool names = %+v", state.ToolNames)
	}
}
