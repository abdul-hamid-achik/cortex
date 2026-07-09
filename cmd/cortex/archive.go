/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var archiveCmd = &cobra.Command{
	Use:   "archive <taskId>",
	Short: "Retire a finished session — move it out of the active view (reversible, no data loss)",
	Long: `Move a terminal (complete / abandoned / blocked) session out of the active
sessions tree into the archive, so cortex sessions / overview / studio stay
focused on live work. The data is preserved and fully reversible with
cortex unarchive — nothing is deleted. In-flight sessions are refused.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := kernel.ArchiveSession(args[0])
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(map[string]string{"archived": args[0], "repo": slug})
		}
		pln(os.Stdout, paint(styOK, "✓ archived ")+args[0]+
			paint(styMuted, " ("+slug+") — restore with `cortex unarchive "+args[0]+"`"))
		return nil
	},
}

var unarchiveCmd = &cobra.Command{
	Use:   "unarchive <taskId>",
	Short: "Restore an archived session to the active view",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := kernel.UnarchiveSession(args[0])
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(map[string]string{"unarchived": args[0], "repo": slug})
		}
		pln(os.Stdout, paint(styOK, "✓ restored ")+args[0]+paint(styMuted, " ("+slug+")"))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(unarchiveCmd)
}
