/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Command cortex is a local-first, evidence-guided agent kernel for software-
// engineering agents. It sits between an LLM and a set of specialist tools
// (codemap, vecgrep, cairntrace, glyphrun, fcheap, tvault) and enforces a
// stateful reasoning loop: orient → investigate → plan → change → verify →
// preserve evidence. Three surfaces share one kernel: a CLI (with --json for
// agents), an MCP server (cortex serve), and Studio (cortex studio).
// See AGENTS.md for architecture.
package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/abdul-hamid-achik/cortex/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "cortex",
	Short: "Evidence-guided agent kernel for engineering agents",
	Long: `Cortex is a local-first runtime that sits between an LLM and a set of
specialist tools. It supplies what models are bad at preserving across long
tool-using tasks: stable task state, explicit evidence and uncertainty,
disciplined tool selection, bounded changes, and verification tied to
user-visible behavior.

Core loop actions drive a task:
  open        retry-safely resume matching work or start and orient a case
  investigate route a question through discovery then structure; record evidence
  plan        state a hypothesis + disproof path + change boundary + verify plan
  begin-change claim bounded change ownership for a stable actor before editing
  verify      run required verifiers as the lease actor and detect scope drift
  remember    persist the outcome and complete the task
  status      phase, unresolved hypotheses, scope drift, missing verification

Use start only when a deliberately fresh case is required. Agent-facing JSON
results include structured actions describing the next safe continuation.

Three surfaces share one kernel: this CLI (--json for agents), the MCP server
(cortex serve), and the cross-workspace Studio board (cortex studio).`,
	Version:       version.Full(),
	SilenceUsage:  true,
	SilenceErrors: false,
}

func init() {
	rootCmd.PersistentFlags().StringP("workspace", "C", "", "workspace/repository directory (defaults to cwd)")
	rootCmd.PersistentFlags().Bool("json", false, "emit machine-readable JSON instead of the styled view")
}

// kernelFor builds a kernel for the resolved workspace directory.
func kernelFor(cmd *cobra.Command) (*kernel.Kernel, error) {
	return kernel.New(config.For(workspaceArg(cmd)))
}

// workspaceArg returns the inherited -C/--workspace value for commands and
// completion callbacks. An empty value intentionally retains config.For's cwd
// default.
func workspaceArg(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	ws, _ := cmd.Flags().GetString("workspace")
	return ws
}

// jsonMode reports whether --json was requested.
func jsonMode(cmd *cobra.Command) bool {
	b, _ := cmd.Flags().GetBool("json")
	return b
}

// fail prints an error to stderr and returns it so main sets a non-zero exit.
func fail(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
