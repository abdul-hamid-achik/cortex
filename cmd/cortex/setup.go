/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

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

setup is read-only unless --trust-commands is passed. It never runs indexing
(which can be long-running and, for vecgrep, needs a local embedding service).
Run cortex init first if you have no cortex.yaml.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		trust, _ := cmd.Flags().GetBool("trust-commands")
		yes, _ := cmd.Flags().GetBool("yes")
		if trust {
			return runTrustCommands(cmd, k, yes)
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

func runTrustCommands(cmd *cobra.Command, k *kernel.Kernel, yes bool) error {
	verifiers := k.ConfiguredCommandVerifiers()
	if len(verifiers) == 0 {
		return fmt.Errorf("no command verifiers configured in this workspace")
	}
	w := os.Stderr
	fmt.Fprintln(w, "Granting these argv arrays (stored as digests in Cortex config, not the repo):")
	for name, argv := range verifiers {
		fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(argv, " "))
	}
	if !yes {
		if err := confirmTrust(os.Stdin, os.Stderr); err != nil {
			return err
		}
	}
	grants, err := k.TrustCommandVerifiers()
	if err != nil {
		return err
	}
	if jsonMode(cmd) {
		return emitJSON(map[string]any{"grants": grants})
	}
	pln(os.Stdout, heading("command grants"))
	for _, grant := range grants {
		pf(os.Stdout, "  %s %s\n", paint(styOK, "✓"), grant.Name+" → "+grant.ArgvDigest)
	}
	return nil
}

func confirmTrust(in *os.File, out *os.File) error {
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("refusing to grant command verifiers without a TTY; pass --yes from a trusted launcher")
	}
	fmt.Fprint(out, "Grant these command verifiers for this workspace? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("command grants not written")
	}
}

func init() {
	setupCmd.Flags().Bool("trust-commands", false, "grant configured command-verifier argv for this workspace (digests stored outside the repo)")
	setupCmd.Flags().Bool("yes", false, "skip the TTY confirmation (trusted launcher only)")
	rootCmd.AddCommand(setupCmd)
}
