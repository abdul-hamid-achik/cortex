/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate a legacy ~/.cortex layout into the split XDG directories",
	Long: `A user who still has ~/.cortex keeps running Cortex's old collapsed
layout (config.yaml, sessions/, archive/, cache/ all under one directory). This
command moves that legacy tree into the modern split locations —
$XDG_CONFIG_HOME/cortex, $XDG_STATE_HOME/cortex, $XDG_CACHE_HOME/cortex — so
Cortex resolves on the current layout going forward.

This is a DRY RUN by default: it reports what would move without touching
anything. Pass --apply to actually perform the moves. It is all-or-nothing: if
any XDG destination already exists, the whole migration is blocked (nothing is
moved) so it can't leave a half-migrated state — resolve the conflict and re-run.
If ~/.cortex is empty after moving, it is removed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		apply, _ := cmd.Flags().GetBool("apply")
		rep, err := kernel.Migrate(apply)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(rep)
		}

		w := os.Stdout
		// No-op cases (CORTEX_HOME set / no legacy base): just the note.
		if len(rep.Moves) == 0 {
			if rep.Note != "" {
				pln(w, paint(styMuted, rep.Note))
			}
			return nil
		}

		pf(w, "%s %s\n", paint(styLabel, "legacy base"), rep.Base)
		pln(w, heading("Moves"))
		for _, mv := range rep.Moves {
			switch {
			case mv.Skipped != "":
				pf(w, "  %s %s -> %s (%s)\n", paint(styErr, "✗ conflict"), mv.From, mv.To, mv.Skipped)
			case rep.Applied:
				pf(w, "  %s %s -> %s\n", paint(styOK, "✓ moved"), mv.From, mv.To)
			default:
				pf(w, "  %s %s -> %s\n", paint(styMuted, "  would move"), mv.From, mv.To)
			}
		}

		pln(w)
		if rep.Note != "" { // blocked on conflicts (all-or-nothing) — nothing moved
			pln(w, paint(styWarn, "⚠ "+rep.Note))
			return nil
		}
		if !rep.Applied {
			pln(w, paint(styMuted, "dry run — nothing was moved. Re-run with --apply to perform the migration."))
			return nil
		}
		if rep.RemovedBase {
			pln(w, paint(styOK, "✓ ")+rep.Base+paint(styMuted, " is empty and was removed"))
		} else {
			pln(w, paint(styWarn, "⚠ "+rep.Base+" was left in place (see errors above)"))
		}
		return nil
	},
}

func init() {
	migrateCmd.Flags().Bool("apply", false, "perform the migration (default is a dry run)")
	rootCmd.AddCommand(migrateCmd)
}
