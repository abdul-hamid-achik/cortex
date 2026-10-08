//go:build unix

/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

const testModel = "claude-test-model"

// rig is one test launcher setup: a workspace, a fake agent that replays a
// canned stream-json file, and a directory where the fake records what it saw.
type rig struct {
	t         *testing.T
	root      string
	workspace string
	out       string
	agent     string
	cortex    string
	tool      string
	roots     map[string]string
}

func stringLine(t *testing.T, value map[string]any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func initLine(t *testing.T, model string) string {
	return stringLine(t, map[string]any{"type": "system", "subtype": "init", "model": model})
}

func toolLine(t *testing.T, id string) string {
	return stringLine(t, map[string]any{"type": "assistant", "message": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "working"}, map[string]any{"type": "tool_use", "id": id, "name": "Bash"}},
	}})
}

func resultLine(t *testing.T, text string, cost float64) string {
	return stringLine(t, map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": text, "total_cost_usd": cost,
		"usage": map[string]any{"input_tokens": 100, "cache_creation_input_tokens": 20, "cache_read_input_tokens": 30, "output_tokens": 7},
	})
}

func happyStream(t *testing.T) []string {
	return []string{
		initLine(t, testModel), toolLine(t, "t1"), toolLine(t, "t2"),
		resultLine(t, "Done.\nCOMPLETION: verified", 0.1234565),
	}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{t: t, root: root, workspace: filepath.Join(root, "workspace"), out: filepath.Join(root, "out"), roots: map[string]string{}}
	for _, dir := range []string{r.workspace, r.out} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range armRootNames {
		dir := filepath.Join(root, "arm", strings.ToLower(name))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		r.roots[name] = dir
	}
	r.cortex = r.script("cortex-bin", "echo cortex 9.9.9")
	r.tool = r.script("git-bin", "echo git version 1.0")
	return r
}

func (r *rig) script(name, body string) string {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		r.t.Fatal(err)
	}
	return path
}

// installAgent writes the fake claude: it records its argv, environment, cwd,
// shim directory, and MCP config, then replays the stream and runs tail.
func (r *rig) installAgent(stream []string, tail string) {
	r.t.Helper()
	streamFile := filepath.Join(r.root, "stream.jsonl")
	if err := os.WriteFile(streamFile, []byte(strings.Join(stream, "\n")+"\n"), 0o600); err != nil {
		r.t.Fatal(err)
	}
	body := fmt.Sprintf(`if [ "$1" = "--version" ]; then echo "fake-claude 1.2.3"; echo second line; exit 0; fi
OUT=%q
printf '%%s\0' "$@" > "$OUT/args.bin"
env | sort > "$OUT/env.txt"
pwd -P > "$OUT/pwd.txt"
ls "${PATH%%%%:*}" > "$OUT/shim.txt"
prev=
for a in "$@"; do
  if [ "$prev" = "--mcp-config" ]; then cp "$a" "$OUT/mcp.json"; fi
  prev=$a
done
cat %q
%s
`, r.out, streamFile, tail)
	r.agent = r.script("claude-bin", body)
}

func (r *rig) request(arm trajectory.Arm) trajectory.LauncherRequest {
	return trajectory.LauncherRequest{
		SchemaVersion: trajectory.ProtocolSchemaVersion,
		Arm:           arm,
		Workspace:     r.workspace,
		Goal:          "make the greeting configurable",
		Acceptance: []domain.AcceptanceCriterion{
			{ID: "greets", Statement: "greeting is configurable"},
			{ID: "no-commit", Statement: "no commit is created", Kind: domain.CriterionKindProcess},
		},
		Surfaces: []domain.Surface{domain.SurfaceCode},
		Model: trajectory.Model{
			Identifier: testModel, Build: "test", ContextBudgetTokens: 1000,
			TemperatureUnsupportedReason: "claude cli cannot set temperature",
			SeedUnsupportedReason:        "claude cli cannot set a seed",
		},
		Budget: trajectory.Budget{
			MaxToolCalls: 5, MaxWallTime: trajectory.Duration(60 * time.Second),
			MaxOracleWallTime: trajectory.Duration(10 * time.Second), MaxTraceBytes: 1 << 16,
			MaxEstimatedCostMicros: 1_500_000,
		},
	}
}

// encode mirrors the runner: digest over the marshalled JSON, newline appended.
func encode(t *testing.T, request trajectory.LauncherRequest) (stdin []byte, digest string) {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return append(data, '\n'), "sha256:" + hex.EncodeToString(sum[:])
}

func (r *rig) environ(extra ...string) []string {
	environ := []string{
		"ANTHROPIC_API_KEY=sk-test-key", "LANG=C.UTF-8",
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CORTEX_APPROVE_COMMANDS=1", "CORTEX_APPROVE_TRAJECTORY=1",
		"AWS_SECRET_ACCESS_KEY=leak", "GITHUB_TOKEN=leak", "PATH=/should/not/leak",
	}
	for _, name := range armRootNames {
		environ = append(environ, name+"="+r.roots[name])
	}
	return append(environ, extra...)
}

type launched struct {
	code   int
	result trajectory.LauncherResult
	stdout string
	stderr string
	digest string
}

// launch runs the launcher in-process with the given request and flags.
func (r *rig) launch(request trajectory.LauncherRequest, flags []string, environ []string) launched {
	r.t.Helper()
	stdin, digest := encode(r.t, request)
	return r.launchRaw(stdin, digest, flags, environ)
}

func (r *rig) launchRaw(stdin []byte, digest string, flags []string, environ []string) launched {
	r.t.Helper()
	args := append([]string{"--agent", r.agent, "--tool", "git=" + r.tool, "--require-api-key=false"}, flags...)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, bytes.NewReader(stdin), &stdout, &stderr, environ)
	got := launched{code: code, stdout: stdout.String(), stderr: stderr.String(), digest: digest}
	if code == 0 {
		got.result = decodeResultStrict(r.t, stdout.Bytes(), digest)
	}
	return got
}

// decodeResultStrict applies the runner's own acceptance rules: exactly one
// JSON value, no unknown fields, and LauncherResult.Validate.
func decodeResultStrict(t *testing.T, data []byte, digest string) trajectory.LauncherResult {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result trajectory.LauncherResult
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("strict decode: %v\nstdout: %s", err, data)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON value: %s", data)
	}
	if err := result.Validate(digest); err != nil {
		t.Fatalf("result does not validate: %v", err)
	}
	return result
}

func (r *rig) recorded(name string) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.out, name))
	if err != nil {
		r.t.Fatalf("fake agent did not record %s: %v", name, err)
	}
	return string(data)
}

func (r *rig) agentRan() bool {
	_, err := os.Stat(filepath.Join(r.out, "args.bin"))
	return err == nil
}

func (r *rig) agentArgs() []string {
	r.t.Helper()
	return strings.Split(strings.TrimSuffix(r.recorded("args.bin"), "\x00"), "\x00")
}

func argAfter(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func toolchainNames(result trajectory.LauncherResult) []string {
	names := make([]string, len(result.Toolchain))
	for i, tool := range result.Toolchain {
		names[i] = tool.Name
	}
	return names
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
