//go:build unix

/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

func TestRawArmCompletesWithoutCortex(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	got := r.launch(r.request(trajectory.ArmRawTools), []string{"--cortex", r.cortex}, r.environ())
	if got.code != 0 {
		t.Fatalf("exit code %d, stderr: %s", got.code, got.stderr)
	}
	res := got.result
	if res.Status != trajectory.RunCompleted || res.ReportedCompletion != baseeval.CompletionVerified {
		t.Fatalf("status %s completion %s, stderr: %s", res.Status, res.ReportedCompletion, got.stderr)
	}
	if res.ToolCalls != 2 || res.HumanInterventions != 0 || len(res.SelectedVerifiers) != 0 || len(res.Receipts) != 0 {
		t.Fatalf("unexpected counters: %+v", res)
	}
	if res.EstimatedCostMicros == nil || *res.EstimatedCostMicros != 123457 {
		t.Fatalf("cost micros = %v, want 123457 (rounded, not clamped)", res.EstimatedCostMicros)
	}
	if res.InputTokens == nil || *res.InputTokens != 150 || res.OutputTokens == nil || *res.OutputTokens != 7 {
		t.Fatalf("tokens = %v/%v, want 150/7", res.InputTokens, res.OutputTokens)
	}
	if res.Observation != (trajectory.LauncherObservation{}) {
		t.Fatalf("raw arm observation must be all zero, got %+v", res.Observation)
	}

	// Raw arm: cortex is absent from the shim, the toolchain, and the MCP config.
	if names := toolchainNames(res); contains(names, "cortex") || !contains(names, "claude") || !contains(names, "git") {
		t.Fatalf("raw toolchain = %v", names)
	}
	if shim := strings.Fields(r.recorded("shim.txt")); contains(shim, "cortex") || !contains(shim, "git") {
		t.Fatalf("raw shim = %v", shim)
	}
	if strings.TrimSpace(r.recorded("mcp.json")) != `{"mcpServers":{}}` {
		t.Fatalf("raw mcp config = %s", r.recorded("mcp.json"))
	}
	tools, _ := argAfter(r.agentArgs(), "--allowedTools")
	if tools != "Bash,Read,Edit,Write,Glob,Grep" {
		t.Fatalf("raw allowedTools = %q", tools)
	}
	for _, tool := range res.Toolchain {
		if tool.Name == "claude" && tool.Version != "fake-claude 1.2.3" {
			t.Fatalf("claude version = %q, want the first line only", tool.Version)
		}
	}
}

func TestCortexArmExposesCortexOnlyThroughMCPAndShim(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	got := r.launch(r.request(trajectory.ArmCortex), []string{"--cortex", r.cortex}, r.environ())
	if got.code != 0 || got.result.Status != trajectory.RunCompleted {
		t.Fatalf("code %d status %s stderr: %s", got.code, got.result.Status, got.stderr)
	}
	if !contains(toolchainNames(got.result), "cortex") {
		t.Fatalf("cortex arm toolchain = %v", toolchainNames(got.result))
	}
	if !contains(strings.Fields(r.recorded("shim.txt")), "cortex") {
		t.Fatal("cortex arm shim must contain cortex")
	}
	tools, _ := argAfter(r.agentArgs(), "--allowedTools")
	if tools != "Bash,Read,Edit,Write,Glob,Grep,mcp__cortex" {
		t.Fatalf("cortex allowedTools = %q", tools)
	}

	var config mcpFile
	if err := json.Unmarshal([]byte(r.recorded("mcp.json")), &config); err != nil {
		t.Fatal(err)
	}
	server, ok := config.MCPServers["cortex"]
	if !ok || len(config.MCPServers) != 1 {
		t.Fatalf("mcp servers = %+v", config.MCPServers)
	}
	if server.Command != r.cortex || len(server.Args) != 1 || server.Args[0] != "serve" {
		t.Fatalf("mcp server = %+v", server)
	}
	if server.Env["CORTEX_APPROVE_COMMANDS"] != "1" {
		t.Fatal("mcp server env must carry CORTEX_APPROVE_COMMANDS=1")
	}
	for name, want := range r.roots {
		if server.Env[name] != want {
			t.Fatalf("mcp env %s = %q, want %q", name, server.Env[name], want)
		}
	}
	if !strings.HasSuffix(strings.Split(server.Env["PATH"], ":")[0], "/bin") || !strings.Contains(server.Env["PATH"], childBasePath) {
		t.Fatalf("mcp PATH = %q", server.Env["PATH"])
	}
}

func TestChildEnvironmentIsAnAllowlist(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	got := r.launch(r.request(trajectory.ArmCortex), []string{"--cortex", r.cortex}, r.environ())
	if got.result.Status != trajectory.RunCompleted {
		t.Fatalf("status %s: %s", got.result.Status, got.stderr)
	}
	env := newHostEnv(strings.Split(strings.TrimSpace(r.recorded("env.txt")), "\n"))
	allowed := map[string]bool{
		"HOME": true, "PATH": true, "TMPDIR": true, "ANTHROPIC_API_KEY": true, "LANG": true, "LC_ALL": true,
		// Added by the shell or by os/exec rather than by the launcher.
		"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true,
	}
	for _, name := range armRootNames {
		allowed[name] = true
	}
	for name := range env {
		if !allowed[name] {
			t.Errorf("unexpected variable %s leaked into the agent environment", name)
		}
	}
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CORTEX_APPROVE_COMMANDS", "CORTEX_APPROVE_TRAJECTORY", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"} {
		if _, ok := env[name]; ok {
			t.Errorf("%s must be stripped from the agent environment", name)
		}
	}
	if env["ANTHROPIC_API_KEY"] != "sk-test-key" {
		t.Error("ANTHROPIC_API_KEY must be passed to the agent")
	}
	if !strings.HasSuffix(strings.Split(env["PATH"], ":")[0], "/bin") || strings.Contains(env["PATH"], "/should/not/leak") {
		t.Errorf("PATH = %q", env["PATH"])
	}
	if env["HOME"] == os.Getenv("HOME") || strings.HasPrefix(env["HOME"], r.workspace) {
		t.Errorf("HOME must be a private directory, got %q", env["HOME"])
	}
	if _, err := os.Stat(env["HOME"]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("private HOME must be removed afterwards, stat error: %v", err)
	}
	if pwd := strings.TrimSpace(r.recorded("pwd.txt")); pwd != realPath(t, r.workspace) {
		t.Errorf("agent cwd = %q, want the workspace %q", pwd, r.workspace)
	}
	for name, want := range r.roots {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestAgentArguments(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	request := r.request(trajectory.ArmRawTools)
	got := r.launch(request, nil, r.environ())
	if got.result.Status != trajectory.RunCompleted {
		t.Fatalf("status %s: %s", got.result.Status, got.stderr)
	}
	args := r.agentArgs()
	for flag, want := range map[string]string{
		"--output-format": "stream-json", "--setting-sources": "", "--model": testModel,
		"--permission-mode": "acceptEdits", "--max-budget-usd": "1.500000",
	} {
		if value, ok := argAfter(args, flag); !ok || value != want {
			t.Errorf("%s = %q (present %v), want %q", flag, value, ok, want)
		}
	}
	for _, flag := range []string{"--verbose", "--bare", "--strict-mcp-config", "--no-session-persistence"} {
		if !contains(args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	if prompt, _ := argAfter(args, "-p"); prompt != buildPrompt(request) {
		t.Error("-p must carry the rendered prompt")
	}

	request.Budget.MaxEstimatedCostMicros = 0
	_ = os.Remove(filepath.Join(r.out, "args.bin"))
	r.launch(request, nil, r.environ())
	if contains(r.agentArgs(), "--max-budget-usd") {
		t.Error("a zero cost budget must not pass --max-budget-usd")
	}
}

func TestPromptIsIdenticalAcrossArmsAndNeverMentionsCortex(t *testing.T) {
	r := newRig(t)
	raw, cortex := r.request(trajectory.ArmRawTools), r.request(trajectory.ArmCortex)
	if buildPrompt(raw) != buildPrompt(cortex) {
		t.Fatal("prompts differ between arms")
	}
	prompt := buildPrompt(raw)
	for _, want := range []string{"make the greeting configurable", "greets: greeting is configurable", "no-commit: no commit is created (kind=process", "code", "COMPLETION: verified|unverified|failed|incomplete"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(strings.ToLower(prompt), "cortex") {
		t.Errorf("prompt must not mention cortex:\n%s", prompt)
	}
}

func TestMissingAPIKeyBlocksBeforeRunning(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	environ := []string{}
	args := []string{"--agent", r.agent} // --require-api-key defaults to true
	stdin, digest := encode(t, r.request(trajectory.ArmRawTools))
	var stdout, stderr strings.Builder
	code := run(t.Context(), args, strings.NewReader(string(stdin)), &stdout, &stderr, environ)
	if code != 0 {
		t.Fatalf("blocked is still a valid result; exit %d: %s", code, stderr.String())
	}
	result := decodeResultStrict(t, []byte(stdout.String()), digest)
	if result.Status != trajectory.RunBlocked || r.agentRan() {
		t.Fatalf("status %s, agent ran: %v", result.Status, r.agentRan())
	}
	if !strings.Contains(stderr.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("stderr should explain the block: %s", stderr.String())
	}
}

func TestPinnedSamplingControlsAreBlocked(t *testing.T) {
	temperature, seed := 0.0, int64(7)
	cases := map[string]func(*trajectory.Model){
		"temperature":     func(m *trajectory.Model) { m.Temperature = &temperature; m.TemperatureUnsupportedReason = "" },
		"seed":            func(m *trajectory.Model) { m.Seed = &seed; m.SeedUnsupportedReason = "" },
		"missing reasons": func(m *trajectory.Model) { m.TemperatureUnsupportedReason, m.SeedUnsupportedReason = "", "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.installAgent(happyStream(t), "exit 0")
			request := r.request(trajectory.ArmRawTools)
			mutate(&request.Model)
			got := r.launch(request, nil, r.environ())
			if got.code != 0 || got.result.Status != trajectory.RunBlocked || r.agentRan() {
				t.Fatalf("code %d status %s ran %v: %s", got.code, got.result.Status, r.agentRan(), got.stderr)
			}
		})
	}
}

func TestPreconditionsBlock(t *testing.T) {
	t.Run("unsupported arm", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t), "exit 0")
		got := r.launch(r.request(trajectory.ArmCortexBob), nil, r.environ())
		if got.result.Status != trajectory.RunBlocked || r.agentRan() {
			t.Fatalf("status %s ran %v", got.result.Status, r.agentRan())
		}
	})
	t.Run("cortex arm without --cortex", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t), "exit 0")
		got := r.launch(r.request(trajectory.ArmCortex), nil, r.environ())
		if got.result.Status != trajectory.RunBlocked || r.agentRan() {
			t.Fatalf("status %s ran %v", got.result.Status, r.agentRan())
		}
	})
	t.Run("cortex arm without per-arm roots", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t), "exit 0")
		got := r.launch(r.request(trajectory.ArmCortex), []string{"--cortex", r.cortex}, []string{"ANTHROPIC_API_KEY=k"})
		if got.result.Status != trajectory.RunBlocked || r.agentRan() {
			t.Fatalf("status %s ran %v", got.result.Status, r.agentRan())
		}
	})
	t.Run("scratch inside the workspace", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t), "exit 0")
		t.Setenv("TMPDIR", r.workspace)
		got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunBlocked || r.agentRan() {
			t.Fatalf("status %s ran %v: %s", got.result.Status, r.agentRan(), got.stderr)
		}
	})
	t.Run("malformed request", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t), "exit 0")
		raw := []byte(`{"schemaVersion":1,"bogus":true}` + "\n")
		sum := requestDigest(raw)
		got := r.launchRaw(raw, sum, nil, r.environ())
		if got.code != 0 || got.result.Status != trajectory.RunBlocked || r.agentRan() {
			t.Fatalf("code %d status %s: %s", got.code, got.result.Status, got.stderr)
		}
	})
}

func TestOperatorMisconfigurationExitsNonZero(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	stdin, _ := encode(t, r.request(trajectory.ArmRawTools))
	for name, args := range map[string][]string{
		"no agent":           {},
		"relative agent":     {"--agent", "claude"},
		"reserved tool name": {"--agent", r.agent, "--tool", "cortex=" + r.cortex},
		"bad tool":           {"--agent", r.agent, "--tool", "git"},
		"missing agent file": {"--agent", filepath.Join(r.root, "nope")},
	} {
		var stdout, stderr strings.Builder
		if code := run(t.Context(), args, strings.NewReader(string(stdin)), &stdout, &stderr, nil); code == 0 {
			t.Errorf("%s: expected a non-zero exit", name)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: nothing may be printed on stdout, got %q", name, stdout.String())
		}
	}
}

func TestToolCallCapKillsAgentAndReportsIncomplete(t *testing.T) {
	r := newRig(t)
	stream := []string{initLine(t, testModel)}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		stream = append(stream, toolLine(t, id))
	}
	r.installAgent(stream, "sleep 30")
	request := r.request(trajectory.ArmRawTools)
	request.Budget.MaxToolCalls = 5
	start := time.Now()
	got := r.launch(request, nil, r.environ())
	if time.Since(start) > 20*time.Second {
		t.Fatal("the agent was not killed at the cap")
	}
	if got.result.Status != trajectory.RunIncomplete || got.result.ToolCalls != 5 {
		t.Fatalf("status %s toolCalls %d, stderr: %s", got.result.Status, got.result.ToolCalls, got.stderr)
	}
	if got.result.ReportedCompletion != baseeval.CompletionIncomplete {
		t.Fatalf("completion = %s", got.result.ReportedCompletion)
	}
	if got.result.EstimatedCostMicros != nil || got.result.InputTokens != nil {
		t.Fatal("cost and tokens must stay nil without a result event")
	}
}

func TestModelMismatchFails(t *testing.T) {
	r := newRig(t)
	stream := happyStream(t)
	stream[0] = initLine(t, "some-other-model")
	r.installAgent(stream, "exit 0")
	got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
	if got.result.Status != trajectory.RunFailed {
		t.Fatalf("status %s, want failed", got.result.Status)
	}
	if !strings.Contains(got.stderr, "some-other-model") || !strings.Contains(got.stderr, testModel) {
		t.Fatalf("stderr must log the mismatch: %s", got.stderr)
	}
}

func TestMissingCompletionLineReportsIncomplete(t *testing.T) {
	r := newRig(t)
	stream := happyStream(t)
	stream[len(stream)-1] = resultLine(t, "All finished, trust me.", 0.01)
	r.installAgent(stream, "exit 0")
	got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
	if got.result.Status != trajectory.RunCompleted || got.result.ReportedCompletion != baseeval.CompletionIncomplete {
		t.Fatalf("status %s completion %s", got.result.Status, got.result.ReportedCompletion)
	}
}

func TestAgentFailureModes(t *testing.T) {
	t.Run("non-zero exit", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t), "exit 3")
		got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunFailed {
			t.Fatalf("status %s", got.result.Status)
		}
	})
	t.Run("error result", func(t *testing.T) {
		r := newRig(t)
		stream := []string{initLine(t, testModel), stringLine(t, map[string]any{
			"type": "result", "subtype": "error_during_execution", "is_error": true, "result": "boom", "total_cost_usd": 0.5,
		})}
		r.installAgent(stream, "exit 0")
		got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunFailed || got.result.EstimatedCostMicros == nil || *got.result.EstimatedCostMicros != 500000 {
			t.Fatalf("status %s cost %v", got.result.Status, got.result.EstimatedCostMicros)
		}
	})
	t.Run("budget stop", func(t *testing.T) {
		r := newRig(t)
		stream := []string{initLine(t, testModel), stringLine(t, map[string]any{
			"type": "result", "subtype": "error_max_budget_usd", "is_error": true, "total_cost_usd": 1.6,
		})}
		r.installAgent(stream, "exit 1")
		got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunFailed {
			t.Fatalf("a non-zero exit takes precedence, got %s", got.result.Status)
		}
		r.installAgent(stream, "exit 0")
		got = r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunIncomplete {
			t.Fatalf("status %s, want incomplete", got.result.Status)
		}
		if got.result.EstimatedCostMicros == nil || *got.result.EstimatedCostMicros != 1_600_000 {
			t.Fatalf("observed cost must be reported unclamped: %v", got.result.EstimatedCostMicros)
		}
	})
	t.Run("no result event", func(t *testing.T) {
		r := newRig(t)
		r.installAgent([]string{initLine(t, testModel)}, "exit 0")
		got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunIncomplete {
			t.Fatalf("status %s", got.result.Status)
		}
	})
	t.Run("no init event", func(t *testing.T) {
		r := newRig(t)
		r.installAgent(happyStream(t)[1:], "exit 0")
		got := r.launch(r.request(trajectory.ArmRawTools), nil, r.environ())
		if got.result.Status != trajectory.RunFailed {
			t.Fatalf("an unconfirmed model must not pass as completed, got %s", got.result.Status)
		}
	})
}

func TestDeadlineKillsTheWholeProcessGroup(t *testing.T) {
	old := minAgentDeadline
	minAgentDeadline = 300 * time.Millisecond
	t.Cleanup(func() { minAgentDeadline = old })

	r := newRig(t)
	pidFile := filepath.Join(r.out, "child.pid")
	r.installAgent([]string{initLine(t, testModel)}, "sleep 30 &\necho $! > "+pidFile+"\nsleep 30")
	request := r.request(trajectory.ArmRawTools)
	request.Budget.MaxWallTime = trajectory.Duration(time.Second)
	start := time.Now()
	got := r.launch(request, nil, r.environ())
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("launcher took %s", elapsed)
	}
	if got.result.Status != trajectory.RunTimeout || got.result.ReportedCompletion != baseeval.CompletionIncomplete {
		t.Fatalf("status %s completion %s: %s", got.result.Status, got.result.ReportedCompletion, got.stderr)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background process %d survived the group kill", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCortexArmObservesCaseFiles(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	writeCase(t, filepath.Join(r.roots["CORTEX_CASES_DIR"], "task_one"))
	got := r.launch(r.request(trajectory.ArmCortex), []string{"--cortex", r.cortex}, r.environ())
	if got.result.Status != trajectory.RunCompleted {
		t.Fatalf("status %s: %s", got.result.Status, got.stderr)
	}
	o := got.result.Observation
	if !o.BoundaryDeclared || o.Evidence.Items != 3 || o.Evidence.Sourced != 2 {
		t.Fatalf("evidence/boundary observation = %+v", o)
	}
	if o.Disproof.Hypotheses != 3 || o.Disproof.WithDisproofPath != 2 || o.Disproof.Resolutions != 2 || o.Disproof.EvidenceGroundedResolutions != 1 {
		t.Fatalf("disproof observation = %+v", o.Disproof)
	}
	if o.Recovery != (baseeval.RecoveryObservation{}) {
		t.Fatalf("recovery must stay zero: %+v", o.Recovery)
	}
}

func TestCortexArmWithoutCaseFilesScoresZero(t *testing.T) {
	r := newRig(t)
	r.installAgent(happyStream(t), "exit 0")
	got := r.launch(r.request(trajectory.ArmCortex), []string{"--cortex", r.cortex}, r.environ())
	o := got.result.Observation
	if o.BoundaryDeclared || o.Evidence.Items != 0 || o.Disproof.Hypotheses != 0 {
		t.Fatalf("observation = %+v", o)
	}
}
