/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <taskId>",
	Short: "Show a task's phase, hypotheses, scope drift, and missing verification",
	Args:  cobra.ExactArgs(1),
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
	Short:   "Print a full evidence record by ID (its rawRef points to the raw tool output)",
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
	Short:   "Resolve an evidence rawRef (or artifact reference) to its raw content",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		content, err := k.ReadArtifact(args[0], args[1])
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(map[string]string{"ref": args[1], "content": content})
		}
		pln(os.Stdout, content)
		return nil
	},
}

func init() {
	statusCmd.Flags().String("detail", "standard", "standard | full (full adds tool health)")
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(abortCmd)
	rootCmd.AddCommand(readEvidenceCmd)
	rootCmd.AddCommand(readArtifactCmd)
}
