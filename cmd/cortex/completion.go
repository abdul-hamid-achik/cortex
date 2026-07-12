/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

// completeTaskIDs is a cobra ValidArgsFunction that suggests task IDs (with the
// goal as the shell description) for the first positional argument. Task IDs are
// long Crockford-base32 strings no one types by hand, so `cortex show <TAB>`
// completing them is a real usability win. It reads every session across the
// central store plus the current -C workspace's repo-local/custom store.
func completeTaskIDs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp // only the first arg is a taskId
	}
	seen := map[string]bool{}
	out := []string{}
	appendTask := func(id, goal string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id+"\t"+goal)
	}
	sessions, err := kernel.AllSessions(kernel.SessionFilter{})
	if err == nil {
		for _, s := range sessions {
			appendTask(s.ID, s.Goal)
		}
	}
	// Repo-local/custom cases_dir sessions are intentionally absent from the
	// global walk. Include the current -C workspace through the shared kernel.
	if cmd != nil {
		if k, buildErr := kernelFor(cmd); buildErr == nil {
			if tasks, listErr := k.ListTasks(); listErr == nil {
				for _, task := range tasks {
					appendTask(task.ID, task.Goal)
				}
			}
		}
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

// completeAllTaskIDs suggests IDs from both the active tree and the archive (for `rm`).
func completeAllTaskIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out, _ := completeTaskIDs(cmd, args, toComplete)
	seen := make(map[string]bool, len(out))
	for _, item := range out {
		id, _, _ := strings.Cut(item, "\t")
		seen[id] = true
	}
	if ss, err := kernel.ArchivedSessions(kernel.SessionFilter{}); err == nil {
		for _, s := range ss {
			if !seen[s.ID] {
				out = append(out, s.ID+"\t"+s.Goal)
				seen[s.ID] = true
			}
		}
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
		v, err := kernel.ShowSessionIn(workspaceArg(cmd), args[0])
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
		v, err := kernel.ShowSessionIn(workspaceArg(cmd), args[0])
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

// completeDecisionAnswerArgs completes `decision answer <taskId> <decisionId>`:
// task IDs first, then only the pending decision IDs for that task.
func completeDecisionAnswerArgs(cmd *cobra.Command, args []string, tc string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeTaskIDs(cmd, args, tc)
	}
	if len(args) == 1 {
		view, err := kernel.ShowSessionIn(workspaceArg(cmd), args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]string, 0, len(view.Decisions))
		for _, decision := range view.Decisions {
			if decision.Status == domain.DecisionPending {
				out = append(out, decision.ID+"\t"+decision.Question)
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
		archiveCmd, beginChangeCmd, leaseRenewCmd, leaseReleaseCmd,
		decisionRequestCmd, decisionResumeCmd, handoffCmd,
	} {
		c.ValidArgsFunction = completeTaskIDs
	}
	resolveCmd.ValidArgsFunction = completeResolveArgs
	readEvidenceCmd.ValidArgsFunction = completeReadEvidenceArgs
	decisionAnswerCmd.ValidArgsFunction = completeDecisionAnswerArgs
	unarchiveCmd.ValidArgsFunction = completeArchivedTaskIDs
	rmCmd.ValidArgsFunction = completeAllTaskIDs
}
