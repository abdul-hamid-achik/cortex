/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var handoffCmd = &cobra.Command{
	Use:   "handoff <taskId>",
	Short: "Export a bounded session packet for another person or agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		handoff, err := kernel.BuildHandoffIn(workspaceArg(cmd), args[0], time.Now())
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(handoff)
		}
		content := kernel.RenderHandoffMarkdown(handoff)
		output, _ := cmd.Flags().GetString("output")
		if output == "" || output == "-" {
			_, err = fmt.Fprint(os.Stdout, content)
			return err
		}
		if err := os.WriteFile(output, []byte(content), 0o600); err != nil {
			return err
		}
		_, err = fmt.Fprintln(os.Stdout, output)
		return err
	},
}

func init() {
	handoffCmd.Flags().StringP("output", "o", "", "write Markdown to a file instead of stdout (- for stdout)")
	rootCmd.AddCommand(handoffCmd)
}
