/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/abdul-hamid-achik/cortex/internal/mcp"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:     "serve",
	Aliases: []string{"mcp"},
	Short:   "Run the Cortex MCP server over stdio",
	Long: `Start the Model Context Protocol server. It speaks newline-delimited
JSON-RPC over stdio. The default agent profile exposes 17 focused lifecycle,
evidence, decision, and handoff tools. Use --profile all for the 24-tool surface,
which also includes cross-repository observability and archive administration.

The change workflow is explicit: open_task → investigate → plan → begin_change
with an actor → verify with the same actor → remember. Tool results include
structured actions describing the next safe continuation.

Register it with mcphub:
  mcphub add cortex cortex serve
  mcphub sync --write

All diagnostic logging goes to stderr so stdout stays pure JSON-RPC.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, _ := cmd.Flags().GetString("workspace")
		profile, _ := cmd.Flags().GetString("profile")
		if ws == "" {
			if wd, err := os.Getwd(); err == nil {
				ws = wd
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		server, err := mcp.NewServerWithProfile(ws, profile)
		if err != nil {
			return err
		}
		return server.Run(ctx)
	},
}

func init() {
	serveCmd.Flags().String("profile", "agent", "MCP tool exposure: agent (17 focused tools) | all (24 including operator tools)")
	rootCmd.AddCommand(serveCmd)
}
