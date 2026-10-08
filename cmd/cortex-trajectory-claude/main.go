/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// cortex-trajectory-claude is the trusted arm launcher that runs the Claude
// Code CLI for the empirical trajectory harness. The runner executes it once
// per arm, writes one LauncherRequest line to stdin, and expects exactly one
// LauncherResult JSON value on stdout. Everything else goes to stderr.
//
// The launcher exists to keep the experiment uncontaminated: the agent gets a
// private HOME, an allowlisted environment, a strict MCP configuration, and a
// PATH that exposes Cortex only in the cortex arm.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ())
	stop()
	os.Exit(code)
}

// run is the testable entry point. It returns a process exit code: 0 whenever a
// valid result was printed (including blocked, failed, and timeout results), 2
// for operator misconfiguration, 1 for unrecoverable internal errors.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, environ []string) int {
	started := time.Now()
	logf := func(format string, a ...any) {
		_, _ = fmt.Fprintf(stderr, "cortex-trajectory-claude: "+format+"\n", a...)
	}

	opt, err := parseOptions(args, stderr)
	if err != nil {
		logf("%v", err)
		return 2
	}
	raw, err := readRequest(stdin)
	if err != nil {
		logf("%v", err)
		return 1
	}
	digest := requestDigest(raw)
	host := newHostEnv(environ)

	request, requestErr := decodeRequest(raw)
	if requestErr != nil {
		request = trajectory.LauncherRequest{}
	} else {
		requestErr = validateRequest(request)
	}

	toolchain, err := collectToolchain(ctx, toolchainEntries(opt, request.Arm))
	if err != nil {
		logf("%v", err)
		return 1
	}
	result := trajectory.LauncherResult{
		SchemaVersion:      trajectory.ProtocolSchemaVersion,
		RequestDigest:      digest,
		Status:             trajectory.RunBlocked,
		ReportedCompletion: baseeval.CompletionIncomplete,
		// Echoed so the runner can read the status; when nothing ran, the request
		// model is the only configuration there is to report.
		EffectiveModel: request.Model,
		Toolchain:      toolchain,
	}
	emit := func() int {
		if err := result.Validate(digest); err != nil {
			logf("internal error: invalid result: %v", err)
			return 1
		}
		data, err := json.Marshal(result)
		if err != nil {
			logf("internal error: %v", err)
			return 1
		}
		if _, err := stdout.Write(append(data, '\n')); err != nil {
			logf("write result: %v", err)
			return 1
		}
		return 0
	}
	block := func(format string, a ...any) int {
		logf("blocked: "+format, a...)
		return emit()
	}

	if requestErr != nil {
		return block("%v", requestErr)
	}
	if request.Arm != trajectory.ArmRawTools && request.Arm != trajectory.ArmCortex {
		return block("unsupported arm %q (supported: raw_tools, cortex)", request.Arm)
	}
	if opt.RequireAPIKey && host["ANTHROPIC_API_KEY"] == "" {
		return block("ANTHROPIC_API_KEY is empty; refusing to run without an isolated, authenticated session")
	}
	if err := modelPinsUnsupportedControls(request.Model); err != nil {
		return block("%v", err)
	}
	if !processGroupsSupported {
		return block("process groups are unavailable on this platform, so the agent cannot be contained")
	}
	if request.Arm == trajectory.ArmCortex && opt.Cortex == "" {
		return block("the cortex arm requires --cortex")
	}
	roots, err := armRoots(host, request.Arm)
	if err != nil {
		return block("%v", err)
	}
	box, err := newSandbox(request.Workspace, request.Arm, opt, host, roots)
	if err != nil {
		return block("%v", err)
	}
	defer box.Close()

	remaining := agentDeadline(request.Budget.MaxWallTime.Value(), opt.WallMargin) - time.Since(started)
	if remaining <= 0 {
		result.Status = trajectory.RunTimeout
		logf("no wall-time left to start the agent")
		return emit()
	}
	logf("running %s arm with model %s (tool-call budget %d, deadline %s)",
		request.Arm, request.Model.Identifier, request.Budget.MaxToolCalls, remaining.Round(time.Millisecond))
	outcome := runAgent(ctx, opt.Agent, agentArgs(request, request.Arm, box.MCPConfig), request.Workspace,
		box.Env, request.Budget.MaxToolCalls, remaining, stderr)

	status, reported := classify(request, outcome, func(message string) { logf("%s", message) })
	result.Status, result.ReportedCompletion = status, reported
	result.ToolCalls = min(outcome.Stream.ToolCalls, request.Budget.MaxToolCalls)
	if outcome.Stream.Malformed > 0 || outcome.Stream.Oversized > 0 {
		logf("ignored %d malformed and %d oversized stream lines", outcome.Stream.Malformed, outcome.Stream.Oversized)
	}
	if usage := outcome.Stream.Result; usage != nil {
		result.EstimatedCostMicros = usage.CostMicros
		result.InputTokens, result.OutputTokens = usage.InputTokens, usage.OutputTokens
	}
	if request.Arm == trajectory.ArmCortex {
		result.Observation = observeCases(roots["CORTEX_CASES_DIR"])
	}
	return emit()
}
