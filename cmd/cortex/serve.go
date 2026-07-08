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
JSON-RPC over stdio and exposes the six cognitive tools (start_task,
investigate, plan, verify, remember, status) plus abort_task and read_evidence.

Register it with mcphub:
  mcphub add cortex cortex serve
  mcphub sync --write

All diagnostic logging goes to stderr so stdout stays pure JSON-RPC.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, _ := cmd.Flags().GetString("workspace")
		if ws == "" {
			if wd, err := os.Getwd(); err == nil {
				ws = wd
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return mcp.NewServer(ws).Run(ctx)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
