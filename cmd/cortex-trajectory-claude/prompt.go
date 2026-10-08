/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

// buildPrompt renders the task prompt. It is a pure function of the goal,
// acceptance criteria, and surfaces and deliberately ignores the arm: the only
// difference between arms is the tool exposure, never the instructions.
func buildPrompt(request trajectory.LauncherRequest) string {
	var b strings.Builder
	b.WriteString("Make the following change in the current repository.\n\n")
	b.WriteString("Goal:\n")
	b.WriteString(strings.TrimSpace(request.Goal))
	b.WriteString("\n")
	if len(request.Acceptance) > 0 {
		b.WriteString("\nAcceptance criteria (each must hold when you finish):\n")
		for _, criterion := range request.Acceptance {
			fmt.Fprintf(&b, "- %s: %s", criterion.ID, strings.TrimSpace(criterion.Statement))
			if criterion.Kind == domain.CriterionKindProcess {
				b.WriteString(" (kind=process: about how the work is done, not what the code does)")
			}
			b.WriteString("\n")
		}
	}
	if len(request.Surfaces) > 0 {
		names := make([]string, len(request.Surfaces))
		for i, surface := range request.Surfaces {
			names[i] = string(surface)
		}
		fmt.Fprintf(&b, "\nSurfaces this change touches: %s\n", strings.Join(names, ", "))
	}
	b.WriteString("\nInstructions:\n")
	b.WriteString("- Stay within the goal. Do not make unrelated changes.\n")
	b.WriteString("- Run the checks needed to demonstrate that every criterion holds.\n")
	b.WriteString("- END your final message with exactly one line of the form\n")
	b.WriteString("  COMPLETION: verified|unverified|failed|incomplete\n")
	b.WriteString("  choosing one word: verified means you ran checks that prove every criterion; ")
	b.WriteString("unverified means the change is made but not fully proven; ")
	b.WriteString("failed means you could not make the change work; ")
	b.WriteString("incomplete means you stopped before finishing.\n")
	return b.String()
}
