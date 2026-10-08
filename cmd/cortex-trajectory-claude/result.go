/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

// classify turns what the agent run revealed into a run status and the
// completion label the agent reported. Every decision is logged through note.
func classify(request trajectory.LauncherRequest, outcome agentOutcome, note func(string)) (trajectory.RunStatus, baseeval.CompletionLabel) {
	stream := outcome.Stream
	reported := baseeval.CompletionIncomplete
	if stream.Result != nil && outcome.Killed == killNone {
		reported = parseCompletion(stream.Result.Text)
	}

	if stream.SawInit && stream.InitModel != request.Model.Identifier {
		note(fmt.Sprintf("model mismatch: requested %q but the agent reported %q", request.Model.Identifier, stream.InitModel))
		return trajectory.RunFailed, reported
	}
	switch outcome.Killed {
	case killDeadline:
		note("agent exceeded the launcher deadline and was killed")
		return trajectory.RunTimeout, baseeval.CompletionIncomplete
	case killToolCap:
		note(fmt.Sprintf("agent exceeded the %d tool-call budget and was killed", request.Budget.MaxToolCalls))
		return trajectory.RunIncomplete, baseeval.CompletionIncomplete
	case killInterrupted:
		note("launcher was interrupted; the agent was killed")
		return trajectory.RunIncomplete, baseeval.CompletionIncomplete
	}
	if outcome.ExitErr != nil {
		note(outcome.ExitErr.Error())
		return trajectory.RunFailed, baseeval.CompletionIncomplete
	}
	result := stream.Result
	if result == nil {
		if outcome.ExitCode != 0 {
			note(fmt.Sprintf("agent exited with status %d without a result event", outcome.ExitCode))
			return trajectory.RunFailed, baseeval.CompletionIncomplete
		}
		note("agent exited without a result event")
		return trajectory.RunIncomplete, baseeval.CompletionIncomplete
	}
	if outcome.ExitCode != 0 {
		note(fmt.Sprintf("agent exited with status %d", outcome.ExitCode))
		return trajectory.RunFailed, reported
	}
	if result.IsError {
		note(fmt.Sprintf("agent reported an error result (%s)", result.Subtype))
		if strings.HasPrefix(result.Subtype, "error_max_") {
			// The agent stopped on a turn or spend limit: a budget stop, not a defect.
			return trajectory.RunIncomplete, reported
		}
		return trajectory.RunFailed, reported
	}
	if !stream.SawInit {
		note("agent never reported its model, so the requested model cannot be confirmed")
		return trajectory.RunFailed, reported
	}
	return trajectory.RunCompleted, reported
}
