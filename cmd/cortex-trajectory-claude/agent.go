/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

// minAgentDeadline is the floor for the launcher's own deadline. It is a
// variable only so tests can exercise the timeout path quickly.
var minAgentDeadline = 10 * time.Second

const (
	maxAgentStderr = 64 << 10
	waitDelay      = 3 * time.Second
)

var agentBaseTools = "Bash,Read,Edit,Write,Glob,Grep"

// agentDeadline is the launcher's own wall-clock limit for the agent, measured
// from the launcher start: the scenario budget minus a safety margin, floored.
func agentDeadline(maxWall, margin time.Duration) time.Duration {
	deadline := maxWall - margin
	if deadline < minAgentDeadline {
		deadline = minAgentDeadline
	}
	return deadline
}

// agentArgs builds the isolated, non-interactive claude invocation. Nothing
// here depends on the arm except the MCP config file and the allowed tools.
func agentArgs(request trajectory.LauncherRequest, arm trajectory.Arm, mcpConfigPath, cortexInstructions string) []string {
	allowed := agentBaseTools
	if arm == trajectory.ArmCortex {
		allowed += ",mcp__cortex"
	}
	args := []string{
		"-p", buildPrompt(request),
		"--output-format", "stream-json",
		"--verbose",
		"--bare",
		"--strict-mcp-config",
		"--mcp-config", mcpConfigPath,
		"--setting-sources", "",
		"--no-session-persistence",
		"--model", request.Model.Identifier,
		"--permission-mode", "acceptEdits",
	}
	// The deployed Cortex condition includes the instruction snippet a user
	// would put in CLAUDE.md; --bare skips CLAUDE.md discovery, so it is
	// appended explicitly, and only in the cortex arm.
	if arm == trajectory.ArmCortex && cortexInstructions != "" {
		args = append(args, "--append-system-prompt-file", cortexInstructions)
	}
	if micros := request.Budget.MaxEstimatedCostMicros; micros > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(float64(micros)/1e6, 'f', 6, 64))
	}
	// --allowedTools is variadic, so it goes last.
	return append(args, "--allowedTools", allowed)
}

type killReason string

const (
	killNone        killReason = ""
	killToolCap     killReason = "tool_cap"
	killDeadline    killReason = "deadline"
	killInterrupted killReason = "interrupted"
)

// agentOutcome is everything the launcher learns from one agent run.
type agentOutcome struct {
	Stream   streamState
	Killed   killReason
	ExitCode int
	// ExitErr is a start or wait error that is not a plain non-zero exit.
	ExitErr error
}

// limitedWriter forwards at most limit bytes and silently drops the rest, so a
// chatty agent cannot flood the runner's bounded stderr trace.
type limitedWriter struct {
	mu    sync.Mutex
	w     io.Writer
	limit int
}

func (l *limitedWriter) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit > 0 {
		keep := len(data)
		if keep > l.limit {
			keep = l.limit
		}
		_, _ = l.w.Write(data[:keep])
		l.limit -= keep
	}
	return len(data), nil
}

// runAgent starts the agent in its own process group and supervises it until it
// exits, hits the tool-call cap, reaches the deadline, or the launcher is
// interrupted. The whole group is always killed before returning, so no
// background process the agent started outlives the run.
func runAgent(ctx context.Context, agent string, args []string, workspace string, environ []string,
	maxCalls int, deadline time.Duration, stderr io.Writer) agentOutcome {
	var (
		killMu sync.Mutex
		reason killReason
		pid    int
	)
	kill := func(why killReason) {
		killMu.Lock()
		defer killMu.Unlock()
		if reason == killNone {
			reason = why
		}
		killProcessGroup(pid)
	}
	parser := newStreamParser(maxCalls, func() { kill(killToolCap) })

	command := exec.Command(agent, args...) // #nosec G204 -- operator-supplied absolute agent path
	command.Dir = workspace
	command.Env = environ
	command.Stdout = parser
	command.Stderr = &limitedWriter{w: stderr, limit: maxAgentStderr}
	command.WaitDelay = waitDelay
	setProcessGroup(command)

	killMu.Lock()
	err := command.Start()
	if err == nil {
		pid = command.Process.Pid
	}
	killMu.Unlock()
	if err != nil {
		return agentOutcome{ExitCode: -1, ExitErr: fmt.Errorf("start agent: %w", err)}
	}

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	timer := time.NewTimer(deadline)
	defer timer.Stop()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		kill(killDeadline)
		waitErr = <-done
	case <-ctx.Done():
		kill(killInterrupted)
		waitErr = <-done
	}
	// Reap anything the agent left running in its group.
	killProcessGroup(pid)
	parser.Flush()

	outcome := agentOutcome{Stream: parser.snapshot()}
	killMu.Lock()
	outcome.Killed = reason
	killMu.Unlock()
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
	case errors.As(waitErr, &exitErr):
		outcome.ExitCode = exitErr.ExitCode()
	default:
		outcome.ExitCode = -1
		outcome.ExitErr = waitErr
	}
	return outcome
}
