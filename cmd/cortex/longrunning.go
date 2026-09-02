/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

// emitReport prints a report that embeds the shared envelope: JSON under
// --json, otherwise the styled envelope plus a report-specific body. Like
// emitEnvelope it exits non-zero on a kernel rejection.
func emitReport(cmd *cobra.Command, env domain.Envelope, report any, body func()) error {
	if jsonMode(cmd) {
		if err := emitJSON(report); err != nil {
			return err
		}
	} else {
		renderEnvelope(os.Stdout, env)
		if body != nil && env.OK {
			body()
		}
	}
	if !env.OK {
		msg := env.Error
		if msg == "" {
			msg = env.Summary
		}
		return fail("%s", msg)
	}
	return nil
}

// ---- findings ----

var findingCmd = &cobra.Command{
	Use:   "finding",
	Short: "Record, list, triage, dismiss, or convert durable findings (bugs, improvements, ideas)",
}

var findingAddCmd = &cobra.Command{
	Use:   "add <taskId> <title>",
	Short: "Record a finding backed by evidence ids from the case",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		kind, _ := cmd.Flags().GetString("kind")
		severity, _ := cmd.Flags().GetString("severity")
		detail, _ := cmd.Flags().GetString("detail")
		files, _ := cmd.Flags().GetStringArray("file")
		symbols, _ := cmd.Flags().GetStringArray("symbol")
		evidence, _ := cmd.Flags().GetStringArray("evidence")
		actor, _ := cmd.Flags().GetString("actor")
		sensitive, _ := cmd.Flags().GetBool("sensitive")
		rep, err := k.RecordFinding(kernel.FindingInput{
			TaskID: args[0], Title: joinArgs(args[1:]), Kind: kind, Severity: severity, Detail: detail,
			Files: files, Symbols: symbols, Evidence: evidence, Actor: actor, Sensitive: sensitive,
		})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderFindings(rep) })
	},
}

var findingListCmd = &cobra.Command{
	Use:   "list <taskId>",
	Short: "List a case's findings",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		status, _ := cmd.Flags().GetString("status")
		rep, err := k.ListFindings(args[0], status)
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderFindings(rep) })
	},
}

func findingStatusCommand(use, short, status string) *cobra.Command {
	c := &cobra.Command{
		Use:   use + " <taskId> <findingId>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := kernelFor(cmd)
			if err != nil {
				return err
			}
			reason, _ := cmd.Flags().GetString("reason")
			actor, _ := cmd.Flags().GetString("actor")
			rep, err := k.UpdateFindingStatus(cmd.Context(), kernel.FindingStatusInput{
				TaskID: args[0], FindingID: args[1], Status: status, Reason: reason, Actor: actor,
			})
			if err != nil {
				return err
			}
			return emitReport(cmd, rep.Envelope, rep, func() { renderFindings(rep) })
		},
	}
	c.Flags().String("reason", "", "why (required for dismiss; kept for cross-case recall)")
	c.Flags().String("actor", "", "who made the call")
	return c
}

var findingConvertCmd = &cobra.Command{
	Use:   "convert <taskId> <findingId>",
	Short: "Open a linked child case whose acceptance criterion is the finding",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		actor, _ := cmd.Flags().GetString("actor")
		mode, _ := cmd.Flags().GetString("mode")
		risk, _ := cmd.Flags().GetString("risk")
		surfaces, _ := cmd.Flags().GetStringArray("surface")
		rep, err := k.ConvertFinding(cmd.Context(), kernel.ConvertFindingInput{
			TaskID: args[0], FindingID: args[1], Actor: actor, Mode: mode, Risk: risk, Surfaces: toSurfaces(surfaces),
		})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() {
			if rep.ChildTaskID != "" {
				pf(os.Stdout, "  %s %s\n", paint(styLabel, "child   "), rep.ChildTaskID)
			}
		})
	},
}

func renderFindings(rep kernel.FindingReport) {
	w := os.Stdout
	if rep.Finding != nil && len(rep.Findings) == 0 {
		rep.Findings = []domain.Finding{*rep.Finding}
	}
	if len(rep.Findings) == 0 {
		return
	}
	pln(w, heading("Findings"))
	for _, f := range rep.Findings {
		line := fmt.Sprintf("  %s [%s/%s] %s — %s", f.ID, f.Kind, f.Severity, f.Status, clipLine(f.Title, 90))
		if f.Status == domain.FindingDismissed && f.Reason != "" {
			line += " (" + clipLine(f.Reason, 60) + ")"
		}
		if f.ConvertedTaskID != "" {
			line += " → " + f.ConvertedTaskID
		}
		pln(w, line)
	}
	pf(w, "  %s %d open · %d triaged · %d converted · %d dismissed\n", paint(styLabel, "counts  "),
		rep.Counts.Open, rep.Counts.Triaged, rep.Counts.Converted, rep.Counts.Dismissed)
}

// ---- dossier ----

var dossierCmd = &cobra.Command{
	Use:   "dossier",
	Short: "Repository memory: evidence-backed entries per module that outlive cases",
}

var dossierAddCmd = &cobra.Command{
	Use:   "add <taskId>",
	Short: "Write or update a dossier entry from an active case",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		module, _ := cmd.Flags().GetString("module")
		kind, _ := cmd.Flags().GetString("kind")
		title, _ := cmd.Flags().GetString("title")
		summary, _ := cmd.Flags().GetString("summary")
		files, _ := cmd.Flags().GetStringArray("file")
		evidence, _ := cmd.Flags().GetStringArray("evidence")
		actor, _ := cmd.Flags().GetString("actor")
		entryID, _ := cmd.Flags().GetString("entry")
		rep, err := k.UpsertDossierEntry(cmd.Context(), kernel.DossierEntryInput{
			TaskID: args[0], EntryID: entryID, Module: module, Kind: kind, Title: title, Summary: summary,
			Files: files, Evidence: evidence, Actor: actor,
		})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderDossier(rep) })
	},
}

var dossierListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the repository dossier (freshness evaluated against HEAD)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		module, _ := cmd.Flags().GetString("module")
		kind, _ := cmd.Flags().GetString("kind")
		stale, _ := cmd.Flags().GetBool("stale")
		limit, _ := cmd.Flags().GetInt("limit")
		rep, err := k.Dossier(cmd.Context(), kernel.DossierListInput{Module: module, Kind: kind, StaleOnly: stale, Limit: limit})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderDossier(rep) })
	},
}

var dossierRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Recompute and persist stale marks for every dossier entry",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		rep, err := k.RefreshDossier(cmd.Context())
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderDossier(rep) })
	},
}

func renderDossier(rep kernel.DossierReport) {
	w := os.Stdout
	pf(w, "  %s %s (%d entries, %d stale)\n", paint(styLabel, "dossier "), rep.Path, rep.Total, rep.Stale)
	entries := rep.Entries
	if rep.Entry != nil && len(entries) == 0 {
		entries = []domain.DossierEntry{*rep.Entry}
	}
	for _, e := range entries {
		marker := ""
		if e.Stale {
			marker = paint(styWarn, " stale")
		}
		pf(w, "  %s [%s] %s — %s%s\n", e.ID, e.Kind, e.Module, clipLine(e.Title, 70), marker)
	}
}

// ---- coverage ----

var coverageCmd = &cobra.Command{
	Use:   "coverage <taskId>",
	Short: "Survey progress: which modules were explored, summarized, or never seen",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		rep, err := k.Coverage(args[0], all)
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() {
			w := os.Stdout
			pln(w, heading("Modules"))
			for _, m := range rep.Modules {
				pf(w, "  %-11s fan-in %-5d rounds %-3d %s\n", m.Status, m.FanIn, m.Rounds, m.Path)
			}
		})
	},
}

// ---- workplan ----

var workplanCmd = &cobra.Command{
	Use:   "workplan",
	Short: "Campaigns: plan delegable child work under a parent and hand it out",
}

var workplanAddCmd = &cobra.Command{
	Use:   "add <taskId> <goal>",
	Short: "Add a work item to the parent's plan",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		id, _ := cmd.Flags().GetString("id")
		mode, _ := cmd.Flags().GetString("mode")
		risk, _ := cmd.Flags().GetString("risk")
		surfaces, _ := cmd.Flags().GetStringArray("surface")
		files, _ := cmd.Flags().GetStringArray("file")
		after, _ := cmd.Flags().GetStringArray("after")
		criterionFlags, _ := cmd.Flags().GetStringArray("criterion")
		criteria, err := parseAcceptanceCriteria(criterionFlags)
		if err != nil {
			return err
		}
		rep, err := k.AddWorkItem(kernel.WorkItemInput{
			TaskID: args[0], ID: id, Goal: joinArgs(args[1:]), Mode: mode, Risk: risk,
			Surfaces: toSurfaces(surfaces), Files: files, Criteria: criteria, DependsOn: after,
		})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderWorkplan(rep) })
	},
}

var workplanListCmd = &cobra.Command{
	Use:   "list <taskId>",
	Short: "Show the campaign with each item's derived state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		rep, err := k.Workplan(args[0])
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderWorkplan(rep) })
	},
}

var workplanNextCmd = &cobra.Command{
	Use:   "next <taskId>",
	Short: "Claim the next ready item for an actor as a linked child case",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		actor, _ := cmd.Flags().GetString("actor")
		item, _ := cmd.Flags().GetString("item")
		rep, err := k.NextWorkItem(cmd.Context(), kernel.NextWorkItemInput{TaskID: args[0], Actor: actor, ItemID: item})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderWorkplan(rep) })
	},
}

func renderWorkplan(rep kernel.WorkplanReport) {
	w := os.Stdout
	if rep.ChildTask != "" {
		pf(w, "  %s %s\n", paint(styLabel, "child   "), rep.ChildTask)
	}
	if len(rep.Items) == 0 {
		return
	}
	pln(w, heading("Work items"))
	for _, item := range rep.Items {
		line := fmt.Sprintf("  %-8s %s — %s", item.Derived, item.ID, clipLine(item.Goal, 70))
		if item.TaskID != "" {
			line += " → " + item.TaskID
		}
		if len(item.BlockedBy) > 0 {
			line += " (after " + strings.Join(item.BlockedBy, ", ") + ")"
		}
		pln(w, line)
	}
}

// ---- resume ----

var resumeCmd = &cobra.Command{
	Use:   "resume <taskId>",
	Short: "Checkpoint packet plus everything that changed since a cursor (after context loss)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		sinceRaw, _ := cmd.Flags().GetString("since")
		limit, _ := cmd.Flags().GetInt("limit")
		var since time.Time
		if strings.TrimSpace(sinceRaw) != "" {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(sinceRaw))
			if err != nil {
				return fail("--since must be RFC3339 (e.g. 2026-09-02T10:00:00Z): %v", err)
			}
			since = parsed
		}
		rep, err := k.Resume(cmd.Context(), kernel.ResumeInput{TaskID: args[0], Since: since, Limit: limit})
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() {
			w := os.Stdout
			pln(w, "")
			pln(w, rep.Checkpoint)
			if len(rep.Evidence) > 0 {
				pln(w, heading("Evidence since cursor"))
				for _, f := range rep.Evidence {
					pf(w, "  %s %s\n", confBadge(f.Confidence), clipLine(f.Claim, 100))
				}
			}
			if len(rep.StaleEvidence) > 0 {
				pln(w, heading("Stale evidence"))
				for _, s := range rep.StaleEvidence {
					pf(w, "  %s %s — %s\n", s.ID, s.File, s.Reason)
				}
			}
			pf(w, "  %s %s\n", paint(styLabel, "cursor  "), rep.Cursor.Format(time.RFC3339))
		})
	},
	ValidArgsFunction: completeTaskIDs,
}

// ---- jobs ----

var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Detached background investigations (list, cancel; run is the worker entry point)",
}

var jobListCmd = &cobra.Command{
	Use:   "list <taskId>",
	Short: "List a case's background jobs (dead workers are reported as failed)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		rep, err := k.ListJobs(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderJobs(rep) })
	},
}

var jobCancelCmd = &cobra.Command{
	Use:   "cancel <taskId> <jobId>",
	Short: "Cancel a background job; evidence already recorded is kept",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		rep, err := k.CancelJob(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		return emitReport(cmd, rep.Envelope, rep, func() { renderJobs(rep) })
	},
}

var jobRunCmd = &cobra.Command{
	Use:    "run <taskId> <jobId>",
	Short:  "Worker entry point: execute a queued job in this process",
	Args:   cobra.ExactArgs(2),
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		return k.RunJob(cmd.Context(), args[0], args[1])
	},
}

func renderJobs(rep kernel.JobReport) {
	w := os.Stdout
	jobs := rep.Jobs
	if rep.Job != nil && len(jobs) == 0 {
		jobs = []domain.Job{*rep.Job}
	}
	if len(jobs) == 0 {
		return
	}
	pln(w, heading("Jobs"))
	for _, j := range jobs {
		line := fmt.Sprintf("  %s %-8s %d/%d rounds", j.ID, j.Status, j.RoundsDone, j.RoundsTotal)
		if len(j.Modules) > 0 {
			line += " · " + strings.Join(clipStrings(j.Modules, 4), ", ")
		} else if j.Question != "" {
			line += " · " + clipLine(j.Question, 60)
		}
		if j.Error != "" {
			line += " · " + paint(styErr, clipLine(j.Error, 60))
		}
		pln(w, line)
	}
}

func clipStrings(values []string, max int) []string {
	if len(values) <= max {
		return values
	}
	out := append([]string(nil), values[:max]...)
	return append(out, fmt.Sprintf("… %d more", len(values)-max))
}

func init() {
	findingAddCmd.Flags().String("kind", "bug", "bug | improvement | feature | question")
	findingAddCmd.Flags().String("severity", "medium", "low | medium | high")
	findingAddCmd.Flags().String("detail", "", "longer description")
	findingAddCmd.Flags().StringArray("file", nil, "file involved (repeatable)")
	findingAddCmd.Flags().StringArray("symbol", nil, "symbol involved (repeatable)")
	findingAddCmd.Flags().StringArray("evidence", nil, "evidence id from this case (repeatable)")
	findingAddCmd.Flags().String("actor", "", "who recorded it")
	findingAddCmd.Flags().Bool("sensitive", false, "mark the finding as sensitive")
	findingListCmd.Flags().String("status", "", "open | triaged | converted | dismissed")
	findingConvertCmd.Flags().String("actor", "", "actor for the child case")
	findingConvertCmd.Flags().String("mode", "change", "child case mode: change | investigate | review")
	findingConvertCmd.Flags().String("risk", "", "child case risk (defaults to the finding severity)")
	findingConvertCmd.Flags().StringArray("surface", nil, "child case surface (repeatable; defaults to the parent's)")
	findingCmd.AddCommand(findingAddCmd, findingListCmd,
		findingStatusCommand("triage", "Mark a finding triaged (acknowledged, not yet acted on)", "triaged"),
		findingStatusCommand("dismiss", "Dismiss a finding with a reason (kept for recall)", "dismissed"),
		findingConvertCmd)
	for _, c := range []*cobra.Command{findingAddCmd, findingListCmd, findingConvertCmd} {
		c.ValidArgsFunction = completeTaskIDs
	}

	dossierAddCmd.Flags().String("module", "", "module or directory the entry describes (required)")
	dossierAddCmd.Flags().String("kind", "architecture", "architecture | invariant | hotspot | convention | question")
	dossierAddCmd.Flags().String("title", "", "one-line title (required)")
	dossierAddCmd.Flags().String("summary", "", "what the module does / which invariant it keeps (required)")
	dossierAddCmd.Flags().StringArray("file", nil, "file the entry depends on (repeatable; defaults to the module)")
	dossierAddCmd.Flags().StringArray("evidence", nil, "evidence id from this case (repeatable)")
	dossierAddCmd.Flags().String("actor", "", "who wrote it")
	dossierAddCmd.Flags().String("entry", "", "existing entry id to update")
	dossierAddCmd.ValidArgsFunction = completeTaskIDs
	dossierListCmd.Flags().String("module", "", "filter by module path prefix")
	dossierListCmd.Flags().String("kind", "", "filter by kind")
	dossierListCmd.Flags().Bool("stale", false, "only entries whose files changed since they were written")
	dossierListCmd.Flags().Int("limit", 0, "max entries (default 50)")
	dossierCmd.AddCommand(dossierAddCmd, dossierListCmd, dossierRefreshCmd)

	coverageCmd.Flags().Bool("all", false, "show every module, not the first 50")
	coverageCmd.ValidArgsFunction = completeTaskIDs

	workplanAddCmd.Flags().String("id", "", "stable item id (generated when empty)")
	workplanAddCmd.Flags().String("mode", "change", "child case mode: change | investigate | review")
	workplanAddCmd.Flags().String("risk", "medium", "low | medium | high")
	workplanAddCmd.Flags().StringArray("surface", nil, "child case surface (repeatable)")
	workplanAddCmd.Flags().StringArray("file", nil, "file the item is expected to touch (repeatable)")
	workplanAddCmd.Flags().StringArray("after", nil, "item id this one depends on (repeatable)")
	workplanAddCmd.Flags().StringArray("criterion", nil, "acceptance criterion for the child as id=statement (repeatable)")
	workplanNextCmd.Flags().String("actor", "", "stable actor claiming the item (required)")
	workplanNextCmd.Flags().String("item", "", "claim a specific item id instead of the first ready one")
	workplanCmd.AddCommand(workplanAddCmd, workplanListCmd, workplanNextCmd)
	for _, c := range []*cobra.Command{workplanAddCmd, workplanListCmd, workplanNextCmd} {
		c.ValidArgsFunction = completeTaskIDs
	}

	resumeCmd.Flags().String("since", "", "RFC3339 cursor; return only records after it")
	resumeCmd.Flags().Int("limit", 0, "max evidence records (default 50)")

	jobCmd.AddCommand(jobListCmd, jobCancelCmd, jobRunCmd)
	jobListCmd.ValidArgsFunction = completeTaskIDs

	rootCmd.AddCommand(findingCmd, dossierCmd, coverageCmd, workplanCmd, resumeCmd, jobCmd)
}
