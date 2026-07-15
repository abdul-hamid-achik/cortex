/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// cortex-trajectory is an opt-in empirical harness, not a Cortex runtime
// surface and not part of release archives.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cortex-trajectory validate|run --manifest <path>")
	}
	switch args[0] {
	case "validate":
		flags := flag.NewFlagSet("validate", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "", "scenario manifest path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("validate does not accept positional arguments")
		}
		if *manifestPath == "" {
			return errors.New("validate requires --manifest")
		}
		manifest, err := trajectory.LoadManifest(*manifestPath)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{
			"ok": true, "schemaVersion": manifest.SchemaVersion,
			"scenarioId": manifest.ID, "arms": manifest.Arms,
			"repositoryDigest": manifest.Repository.Digest,
		})
	case "run":
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "", "scenario manifest path")
		launcherPath := flags.String("launcher", "", "trusted launcher config path")
		stateRoot := flags.String("state-root", "", "trajectory state root override")
		runID := flags.String("run-id", "", "stable run identifier override")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("run does not accept positional arguments")
		}
		if *manifestPath == "" || *launcherPath == "" {
			return errors.New("run requires --manifest and --launcher")
		}
		manifest, err := trajectory.LoadManifest(*manifestPath)
		if err != nil {
			return err
		}
		launcher, err := trajectory.LoadLauncherConfig(*launcherPath)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		report, err := trajectory.Run(ctx, trajectory.RunInput{
			Manifest: manifest, Launcher: launcher, StateRoot: *stateRoot, RunID: *runID,
		})
		if err != nil {
			return err
		}
		return writeJSON(report)
	default:
		return fmt.Errorf("unknown cortex-trajectory command %q", args[0])
	}
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
