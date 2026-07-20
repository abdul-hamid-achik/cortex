/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <taskId>",
	Short: "Show a task's phase, hypotheses, scope drift, and missing verification",
	Long: `The agent-checkpoint view of a task in the current workspace: its phase, unresolved
hypotheses, scope drift, and missing verification (add --detail full for specialist tool health).
For a richer one-screen human view that works from any directory, use cortex show <taskId>; for a
chronological feed use cortex timeline, and for outcome metrics use cortex metrics.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		detail, _ := cmd.Flags().GetString("detail")
		rep, err := k.Status(cmd.Context(), args[0], detail)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitStatusJSON(rep)
		}
		renderStatus(rep)
		if !rep.OK {
			return fail("%s", rep.Error)
		}
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all tasks in the workspace, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		tasks, err := k.ListTasks()
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(tasks)
		}
		if len(tasks) == 0 {
			pln(os.Stdout, paint(styMuted, "no tasks yet — start one with `cortex start \"<goal>\"`"))
			return nil
		}
		for _, t := range tasks {
			pf(os.Stdout, "%s %s %s %s\n",
				paint(styMuted, t.CreatedAt),
				paint(styPhase, fmt.Sprintf("%-13s", t.Phase)),
				paint(styLabel, t.ID),
				clipLine(t.Goal, 70))
		}
		return nil
	},
}

var abortCmd = &cobra.Command{
	Use:   "abort <taskId> <reason>",
	Short: "Stop a task without deleting its evidence",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		env, err := k.AbortTask(args[0], joinArgs(args[1:]))
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

var readEvidenceCmd = &cobra.Command{
	Use:     "read-evidence <taskId> <evidenceId>",
	Aliases: []string{"evidence"},
	Short:   "Print a full evidence record by ID (/raw/ references support bounded preview)",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		ev, err := k.ReadEvidence(args[0], args[1])
		if err != nil {
			return err
		}
		return emitJSON(ev)
	},
}

var readArtifactCmd = &cobra.Command{
	Use:     "read-artifact <taskId> <ref>",
	Aliases: []string{"artifact", "raw"},
	Short:   "Preview a task-owned raw ref or task-referenced fcheap artifact",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		path, _ := cmd.Flags().GetString("path")
		maxBytes, _ := cmd.Flags().GetInt("max-bytes")
		allowBinary, _ := cmd.Flags().GetBool("allow-binary")
		preview, err := k.PreviewArtifactWithOptions(cmd.Context(), args[0], args[1], path, maxBytes, allowBinary)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(preview)
		}
		pln(os.Stdout, preview.Content)
		return nil
	},
}

func init() {
	statusCmd.Flags().String("detail", "standard", "standard | full (full adds tool health)")
	readArtifactCmd.Flags().String("path", "", "safe relative path inside an fcheap stash (empty uses bounded discovery)")
	readArtifactCmd.Flags().Int("max-bytes", kernel.DefaultArtifactPreviewBytes,
		"maximum source bytes to return (hard-capped at 131072)")
	readArtifactCmd.Flags().Bool("allow-binary", false, "explicitly allow bounded binary content as base64")
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(abortCmd)
	rootCmd.AddCommand(readEvidenceCmd)
	rootCmd.AddCommand(readArtifactCmd)
}
