/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

// completeTaskIDs is a cobra ValidArgsFunction that suggests task IDs (with the
// goal as the shell description) for the first positional argument. Task IDs are
// long Crockford-base32 strings no one types by hand, so `cortex show <TAB>`
// completing them is a real usability win. It reads every session across the
// central store, so completion works regardless of the current directory.
func completeTaskIDs(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp // only the first arg is a taskId
	}
	sessions, err := kernel.AllSessions(kernel.SessionFilter{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID+"\t"+s.Goal) // "<completion>\t<description>"
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeArchivedTaskIDs suggests IDs of *archived* sessions (for `unarchive`),
// which the active-tree completer would never surface.
func completeArchivedTaskIDs(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	sessions, err := kernel.ArchivedSessions(kernel.SessionFilter{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID+"\t"+s.Goal)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeResolveArgs completes `resolve <taskId> <hypId>`: task IDs for the
// first arg, then that task's hypothesis IDs (with the statement as description)
// for the second — the hypId is otherwise an unmemorable `hyp_…` string.
func completeResolveArgs(cmd *cobra.Command, args []string, tc string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeTaskIDs(cmd, args, tc)
	}
	if len(args) == 1 {
		v, err := kernel.ShowSession(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]string, 0, len(v.Hypotheses))
		for _, h := range v.Hypotheses {
			out = append(out, h.ID+"\t"+h.Statement)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeReadEvidenceArgs completes `read-evidence <taskId> <evidenceId>`: task
// IDs, then that task's evidence IDs (claim as description) from its timeline.
func completeReadEvidenceArgs(cmd *cobra.Command, args []string, tc string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeTaskIDs(cmd, args, tc)
	}
	if len(args) == 1 {
		v, err := kernel.ShowSession(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]string, 0)
		for _, e := range v.Timeline {
			if e.Kind == "evidence" && e.Ref != "" {
				out = append(out, e.Ref+"\t"+e.Summary)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// init wires dynamic completion onto every command whose first argument is a
// taskId. Centralized here so the individual command files stay focused; the
// command vars are already initialized by the time init() runs. `resolve` and
// `read-evidence` also complete their second (hyp/evidence) ID argument. Generate
// the shell script with `cortex completion {bash|zsh|fish}` (cobra's built-in).
func init() {
	for _, c := range []*cobra.Command{
		statusCmd, showCmd, timelineCmd, metricsCmd, abortCmd,
		readArtifactCmd, investigateCmd, planCmd, verifyCmd, rememberCmd,
		archiveCmd,
	} {
		c.ValidArgsFunction = completeTaskIDs
	}
	resolveCmd.ValidArgsFunction = completeResolveArgs
	readEvidenceCmd.ValidArgsFunction = completeReadEvidenceArgs
	unarchiveCmd.ValidArgsFunction = completeArchivedTaskIDs
}
