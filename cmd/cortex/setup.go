/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Check workspace readiness — git, config, and whether discovery tools are indexed",
	Long: `Report what this workspace still needs for Cortex's full discovery and
verification to work: is it a git repo, is there a cortex.yaml, and are codemap
and vecgrep installed and indexed. For each gap it prints the exact command to
fix it.

setup is read-only — it never runs indexing (which can be long-running and, for
vecgrep, needs a local embedding service). Run cortex init first if you have no
cortex.yaml.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		rep := k.Setup(cmd.Context())
		if jsonMode(cmd) {
			return emitJSON(rep)
		}
		renderSetup(rep)
		return nil
	},
}

func renderSetup(rep kernel.SetupReport) {
	w := os.Stdout
	pln(w, heading("cortex setup"))

	pln(w, heading("Workspace"))
	pf(w, "  %s %s\n", paint(styLabel, "workspace"), rep.Workspace)
	if rep.IsRepo {
		pf(w, "  %s %s\n", paint(styLabel, "git     "), paint(styOK, "✓")+" git repository")
	} else {
		pf(w, "  %s %s\n", paint(styLabel, "git     "), paint(styErr, "○")+" not a git repo — Cortex needs git for identity, diffs, and verification")
	}
	if rep.HasConfig {
		pf(w, "  %s %s\n", paint(styLabel, "config  "), paint(styOK, "✓")+fmt.Sprintf(" cortex.yaml (%d verifier(s))", rep.VerifierCount))
	} else {
		pf(w, "  %s %s\n", paint(styLabel, "config  "), paint(styWarn, "○")+" no cortex.yaml — run "+paint(styLabel, "cortex init")+" to detect your test runner")
	}

	pln(w, heading("Discovery & structure"))
	var fixes []string
	for _, ts := range rep.Tools {
		switch ts.Status {
		case kernel.SetupReady:
			pf(w, "  %s %-9s %s\n", paint(styOK, "●"), ts.Tool, paint(styMuted, "ready (indexed)"))
		case kernel.SetupNeedsIndex:
			pf(w, "  %s %-9s %s\n", paint(styWarn, "○"), ts.Tool, "needs index — run: "+paint(styLabel, ts.FixCommand))
			fixes = append(fixes, ts.Tool+": "+ts.FixCommand)
		case kernel.SetupMissing:
			pf(w, "  %s %-9s %s\n", paint(styErr, "○"), ts.Tool, paint(styMuted, "not on PATH — discovery degrades, but the git-grep fallback still works"))
		default:
			pf(w, "  %s %-9s %s\n", paint(styErr, "○"), ts.Tool, "probe error: "+ts.Detail)
		}
	}

	var steps []string
	if !rep.IsRepo {
		steps = append(steps, "git init && git commit — Cortex needs a git repository")
	}
	if !rep.HasConfig {
		steps = append(steps, "cortex init — write a cortex.yaml with your test runner")
	}
	steps = append(steps, fixes...)

	pln(w)
	if len(steps) == 0 {
		pln(w, paint(styOK, "✓ ready to investigate and verify"))
	} else {
		pln(w, paint(styWarn, fmt.Sprintf("⚠ %d step(s) to unlock full discovery/verification:", len(steps))))
		for _, s := range steps {
			pln(w, "  "+paint(styMuted, "•")+" "+s)
		}
	}
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
