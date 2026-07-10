/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
	"github.com/spf13/cobra"
)

var routeCmd = &cobra.Command{
	Use:   "route [question]",
	Short: "Show the routing matrix or resolve which tools cortex routes a question to",
	Long: `Print the ordered SPEC §7.1 routing matrix, or resolve it for one question.
Pass --json for machine output; --surface (repeatable) overrides detected surfaces
for a resolved question (code, browser, terminal, artifact, secret).`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		surfaceFlags, _ := cmd.Flags().GetStringArray("surface")
		if cmd.Flags().Changed("surface") && len(surfaceFlags) == 0 {
			return fail("invalid surface %q (want code, browser, terminal, artifact, or secret)", "")
		}
		surfaces := make([]string, 0, len(surfaceFlags))
		parsed := make([]domain.Surface, 0, len(surfaceFlags))
		for _, value := range surfaceFlags {
			surface := strings.TrimSpace(value)
			if !validRouteSurface(surface) {
				return fail("invalid surface %q (want code, browser, terminal, artifact, or secret)", surface)
			}
			surfaces = append(surfaces, surface)
			parsed = append(parsed, domain.Surface(surface))
		}
		if len(args) == 0 {
			if len(surfaces) != 0 {
				return fail("--surface requires a question")
			}
			if jsonMode(cmd) {
				return emitJSON(domain.RoutingMatrix)
			}
			renderRoutingMatrix()
			return nil
		}

		route := domain.RouteFor(args[0], parsed)
		ws, _ := cmd.Flags().GetString("workspace")
		cfg := config.For(ws)
		safeQuestion := redact.New(cfg.RedactLiterals...).String(args[0])
		if jsonMode(cmd) {
			return emitJSON(map[string]any{
				"question": safeQuestion, "surfaces": surfaces,
				"first": route.First, "followUp": route.FollowUp, "why": route.Why,
			})
		}
		w := os.Stdout
		pln(w, heading("Route"))
		pf(w, "  %-12s %s\n", "question", clipTo(safeQuestion, 60))
		pf(w, "  %-12s %s\n", "first", paint(styLabel, route.First))
		pf(w, "  %-12s %s\n", "follow-up", paint(styLabel, route.FollowUp))
		pf(w, "  %-12s %s\n", "why", route.Why)
		if len(surfaces) > 0 {
			pf(w, "  %-12s %s\n", "surfaces", strings.Join(surfaces, ", "))
		}
		return nil
	},
}

func validRouteSurface(surface string) bool {
	switch domain.Surface(surface) {
	case domain.SurfaceCode, domain.SurfaceBrowser, domain.SurfaceTerminal, domain.SurfaceArtifact, domain.SurfaceSecret:
		return true
	default:
		return false
	}
}

func renderRoutingMatrix() {
	w := os.Stdout
	pln(w, heading("Routing matrix"))
	for _, rule := range domain.RoutingMatrix {
		pf(w, "  %-34s %s → %s\n", clipTo(rule.Match, 34), paint(styLabel, rule.First), rule.FollowUp)
	}
}

func init() {
	routeCmd.Flags().StringArray("surface", nil, "override detected surfaces (repeatable: code, browser, terminal, artifact, secret)")
	rootCmd.AddCommand(routeCmd)
}
