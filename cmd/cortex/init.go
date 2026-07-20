/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"os"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a starter cortex.yaml, detecting your test runner as a command verifier",
	Long: `Generate a minimal cortex.yaml in the workspace so Cortex can verify against
your own tests out of the box. It detects the project's test runner (Go, Rust,
Node, Python) from marker files and writes a command verifier for it.

Command verifiers stay blocked until the trusted process launching Cortex sets
CORTEX_APPROVE_COMMANDS=1 — repository configuration cannot approve itself. If a
project config already exists, init refuses to overwrite it unless --force is
given.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		force, _ := cmd.Flags().GetBool("force")
		res, err := config.Init(workspaceArg(cmd), force)
		if err != nil {
			return err
		}
		if jsonMode(cmd) {
			return emitJSON(map[string]any{
				"ok":              true,
				"workspace":       res.Workspace,
				"configPath":      res.ConfigPath,
				"created":         res.Created,
				"existed":         res.Existed,
				"existingConfigs": res.Existing,
				"verifiers":       res.Detected,
				"content":         res.Content,
			})
		}
		renderInit(res)
		return nil
	},
}

func renderInit(res config.InitResult) {
	w := os.Stdout
	pln(w, heading("cortex init"))
	if res.Existed && !res.Created {
		pln(w, "  "+paint(styWarn, "skipped")+" a Cortex config already exists — nothing written")
		for _, p := range res.Existing {
			pf(w, "  %s %s\n", paint(styLabel, "found   "), p)
		}
		pln(w, "  "+paint(styMuted, "re-run with --force to overwrite "+res.ConfigPath))
		return
	}
	pf(w, "  %s %s\n", paint(styOK, "✓ wrote"), res.ConfigPath)
	if len(res.Detected) == 0 {
		pln(w, "  "+paint(styWarn, "no known test runner detected — edit the verifier argv by hand"))
	} else {
		pln(w, heading("Detected verifiers"))
		for _, v := range res.Detected {
			pf(w, "  %s  command:%s  %s  %s\n", paint(styLabel, v.Name), v.Name,
				paint(styMuted, "["+strings.Join(v.Argv, " ")+"]"), paint(styMuted, "← "+v.Reason))
		}
	}
	pln(w, heading("Next steps"))
	pln(w, "  1. Review the argv in cortex.yaml.")
	pln(w, "  2. Run Cortex with "+paint(styLabel, "CORTEX_APPROVE_COMMANDS=1")+" set in the trusted launcher.")
	verifier := "unit"
	if len(res.Detected) > 0 {
		verifier = res.Detected[0].Name
	}
	pln(w, "  3. Verify a code claim against it, e.g. "+paint(styLabel, "--claim-verifier command:"+verifier))
	if res.Created && len(res.Existing) > 0 {
		pln(w, "  "+paint(styWarn, "note: other config file(s) also exist and still layer in:"))
		for _, p := range res.Existing {
			pf(w, "    %s\n", p)
		}
	}
}

func init() {
	initCmd.Flags().Bool("force", false, "overwrite an existing cortex.yaml")
	rootCmd.AddCommand(initCmd)
}
