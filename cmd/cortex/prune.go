/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Retire forgotten in-flight sessions — report by default, abort+archive with --apply",
	Long: `List in-flight sessions that have not advanced within --older-than (default
7d). By default this is a dry run: it reports what would be pruned and changes
nothing. With --apply, each stale session is aborted (recording the reason) and
archived out of the active tree, so cortex sessions / overview / studio stay
focused on live work.

Pruning is recoverable: cortex unarchive <taskId> restores an archived session.
The abort records why a session was retired, so forgotten work is shelved
honestly rather than silently hidden. Use --repo to prune one repository only.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		olderThanRaw, _ := cmd.Flags().GetString("older-than")
		olderThan, err := parseAge(olderThanRaw)
		if err != nil {
			return fail("invalid --older-than: %s", err)
		}
		apply, _ := cmd.Flags().GetBool("apply")
		repo, _ := cmd.Flags().GetString("repo")
		rep, err := kernel.PruneStale(time.Now(), olderThan, apply, repo)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(rep)
		}
		renderPrune(rep)
		return nil
	},
}

// parseAge parses a prune threshold: a whole-day suffix ("7d") plus any Go
// duration ("24h", "90m"). time.ParseDuration alone has no day unit.
func parseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("day count must be a positive integer (got %q)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive (got %q)", s)
	}
	return d, nil
}

func renderPrune(rep kernel.PruneStaleReport) {
	w := os.Stdout
	pln(w, heading("cortex prune"))
	age := kernel.FormatAge(rep.OlderThan)
	if len(rep.Stale) == 0 {
		pln(w, paint(styOK, "✓")+" no in-flight sessions older than "+age)
		return
	}
	pf(w, "  %s %d in-flight session(s) older than %s\n", paint(styLabel, "stale "), len(rep.Stale), age)
	for _, s := range rep.Stale {
		pf(w, "  %s %-16s %-12s %s\n", paint(styMuted, s.ID), s.Slug, string(s.Phase), clipLine(s.Goal, 56))
	}
	if !rep.Applied {
		pln(w)
		pln(w, "  "+paint(styMuted, "dry run — re-run with --apply to abort+archive these (recoverable via cortex unarchive)"))
		return
	}
	pln(w)
	pf(w, "  %s %d pruned (aborted + archived)\n", paint(styOK, "✓"), len(rep.Pruned))
	if len(rep.Skipped) > 0 {
		pf(w, "  %s %d skipped (no longer in flight)\n", paint(styMuted, "•"), len(rep.Skipped))
	}
	for _, f := range rep.Failed {
		pf(w, "  %s %s: %s\n", paint(styErr, "✗"), f.TaskID, f.Error)
	}
}

func init() {
	pruneCmd.Flags().String("older-than", "7d", "prune in-flight sessions idle longer than this (e.g. 7d, 24h)")
	pruneCmd.Flags().Bool("apply", false, "actually abort+archive the stale sessions (default is a dry run)")
	pruneCmd.Flags().String("repo", "", "only prune sessions for this repository (slug/name substring)")
	rootCmd.AddCommand(pruneCmd)
}
