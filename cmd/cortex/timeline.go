/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"
	"strconv"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var timelineCmd = &cobra.Command{
	Use:     "timeline <taskId>",
	Aliases: []string{"activity"},
	Short:   "Show a session's chronological activity: phases, evidence, tool calls, verification",
	Long: `Merge a session's phase transitions, evidence, audited tool calls, and
verification receipts into one time-sorted feed — an audit trail of how the case
actually unfolded. Works from any directory; the session is located by task ID.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := kernel.Timeline(args[0])
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(entries)
		}
		renderTimeline(args[0], entries)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(timelineCmd)
}

func renderTimeline(id string, entries []kernel.TimelineEntry) {
	w := os.Stdout
	if len(entries) == 0 {
		pln(w, paint(styMuted, "no activity recorded for "+id))
		return
	}
	pln(w, paint(styHeading, "timeline "+id)+paint(styMuted, "  ("+strconv.Itoa(len(entries))+" events)"))
	for _, e := range entries {
		ts := e.Timestamp.Local().Format("01-02 15:04:05")
		line := paint(styMuted, ts) + "  " + timelineBadge(e.Kind) + "  " + clipLine(e.Summary, 78)
		if e.Detail != "" {
			line += " " + paint(styMuted, "["+e.Detail+"]")
		}
		pln(w, line)
	}
}

// timelineBadge returns an 8-rune, kind-colored badge so the event column aligns.
func timelineBadge(kind string) string {
	switch kind {
	case "phase":
		return paint(styPhase, "◆ phase ")
	case "evidence":
		return paint(styFact, "• evid  ")
	case "command":
		return paint(styMuted, "» cmd   ")
	case "verification":
		return paint(styLabel, "✓ verify")
	default:
		return paint(styMuted, padKind(kind))
	}
}

func padKind(s string) string {
	for len([]rune(s)) < 8 {
		s += " "
	}
	return s
}
