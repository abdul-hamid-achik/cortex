/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the environment: workspace, case store, and specialist tool health",
	Long: `Report Cortex's operating environment — the resolved workspace and case-file
directory — and probe every specialist tool's health. Missing tools are not an
error: their adapters degrade safely and verification on their surface is
blocked rather than fabricated.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		ws, _ := cmd.Flags().GetString("workspace")
		cfg := config.For(ws)
		health := k.Registry().Health(cmd.Context())

		gwServer, _ := cmd.Flags().GetString("gateway-server")
		probe, _ := cmd.Flags().GetBool("probe")
		gw := k.GatewaySelfCheck(cmd.Context(), gwServer, probe)

		// Cross-workspace session snapshot — a monitoring glance at everything
		// Cortex is tracking, not just this repo (nil on error is fine).
		sessions, _ := kernel.AllSessions(kernel.SessionFilter{})
		now := time.Now()
		activeSessions, staleSessions, repos := 0, 0, map[string]bool{}
		for _, s := range sessions {
			if s.Active {
				activeSessions++
			}
			if s.StaleSince(now, 24*time.Hour) {
				staleSessions++
			}
			repos[s.Slug] = true
		}

		if jsonMode(cmd) {
			return emitJSON(map[string]any{
				"workspace": cfg.Workspace,
				"casesDir":  cfg.CasesDir,
				"sessions": map[string]any{
					"total": len(sessions), "active": activeSessions, "stale": staleSessions,
					"repos": len(repos), "root": config.SessionsRoot(),
				},
				"tools":   health,
				"gateway": gw,
			})
		}

		w := os.Stdout
		pln(w, heading("Environment"))
		pf(w, "  %s %s\n", paint(styLabel, "workspace"), cfg.Workspace)
		pf(w, "  %s %s\n", paint(styLabel, "cases   "), cfg.CasesDir)

		pln(w, heading("Sessions"))
		staleCell := fmt.Sprintf("%d stale", staleSessions)
		if staleSessions > 0 {
			staleCell = paint(styWarn, staleCell+" ⚠")
		}
		pf(w, "  %s %d total · %d active · %s · %d repo(s)\n", paint(styLabel, "count"), len(sessions), activeSessions, staleCell, len(repos))
		pf(w, "  %s %s\n", paint(styLabel, "root "), config.SessionsRoot())

		pln(w, heading("Specialist tools"))
		down := 0
		for _, h := range health {
			mark := paint(styOK, "●")
			detail := ""
			if !h.Available {
				mark = paint(styErr, "○")
				detail = paint(styMuted, h.Detail)
				down++
			}
			pf(w, "  %s %-11s %s\n", mark, h.Tool, detail)
		}
		pln(w)
		if down == 0 {
			pln(w, paint(styOK, "✓ all specialist tools available"))
		} else {
			pln(w, paint(styWarn, fmt.Sprintf("⚠ %d tool(s) unavailable — adapters will degrade (verification on their surfaces is blocked, not fabricated)", down)))
		}

		// Gateway registration — advisory only, never changes the exit status.
		pln(w, heading("Gateway registration"))
		switch {
		case !gw.Supported:
			pf(w, "  %s %s\n", paint(styMuted, "—"), paint(styMuted, "gateway self-check unavailable: "+gw.Detail))
		case !gw.Registered:
			pf(w, "  %s %s\n", paint(styWarn, "○"), fmt.Sprintf("%q is NOT registered on the mcphub gateway — run `mcphub add %s cortex serve`", gw.Server, gw.Server))
		default:
			line := fmt.Sprintf("%q registered · enabled=%t · on_path=%t", gw.Server, gw.Enabled, gw.OnPath)
			if gw.ToolCount != nil {
				line += fmt.Sprintf(" · handshake_ok=%t · %d tools", gw.HandshakeOK != nil && *gw.HandshakeOK, *gw.ToolCount)
			}
			pf(w, "  %s %s\n", paint(styOK, "●"), line)
			if gw.Unused || gw.ProxiedCalls == 0 {
				pln(w, "  "+paint(styWarn, "⚠ registered but the gateway has never routed a call to it — check `mcphub sync --write`"))
			}
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().String("gateway-server", adapters.DefaultServerName, "the mcphub-registered server name to self-check")
	doctorCmd.Flags().Bool("probe", false, "complete a real MCP handshake when checking gateway registration")
	rootCmd.AddCommand(doctorCmd)
}
