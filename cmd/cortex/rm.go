/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:     "rm <taskId>",
	Aliases: []string{"delete"},
	Short:   "Permanently delete a session — terminal only, requires --force (DESTRUCTIVE, no undo)",
	Long: `Permanently delete a session's directory and everything under it.

Without --force this is a DRY RUN: it only prints what would be deleted,
nothing is removed. Pass --force to actually delete.

Refuses in-flight (non-terminal) sessions — complete, abort, or archive one
first. Works on a session in either the active tree or the archive.

This is irreversible — unlike cortex archive, which moves a session out of
the active view and can be undone with cortex unarchive. If you just want a
finished session out of the way, prefer archive.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		path, err := kernel.DeleteSession(args[0], force)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(map[string]any{"taskId": args[0], "path": path, "deleted": force})
		}
		if force {
			pln(os.Stdout, paint(styOK, "✓ permanently deleted ")+path)
		} else {
			pln(os.Stdout, paint(styWarn, "would delete ")+path+
				paint(styMuted, " — re-run with --force to permanently delete (no undo)"))
		}
		return nil
	},
}

func init() {
	rmCmd.Flags().Bool("force", false, "actually delete (without this, rm is a dry run)")
	rootCmd.AddCommand(rmCmd)
}
