/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// defaultWallMargin is subtracted from the scenario wall-time budget so the
// launcher still has time to observe case files and print its result before the
// runner's own timeout fires.
const defaultWallMargin = 30 * time.Second

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

// reservedToolNames cannot be exposed through --tool: the agent executable is
// named by --agent, cortex by --cortex, and the remaining managed names belong
// to arms this launcher does not support.
var reservedToolNames = map[string]bool{"claude": true, "cortex": true, "bob": true, "local-agent": true}

type toolSpec struct {
	Name string
	Path string
}

// options is the operator-supplied launcher configuration (argv from the
// trusted launcher.yaml). The scenario manifest cannot influence any of it.
type options struct {
	Agent  string
	Cortex string
	// CortexInstructions is the deployed agent-instruction snippet appended to
	// the system prompt in the cortex arm only (empty = MCP-only condition).
	CortexInstructions string
	Tools              []toolSpec
	RequireAPIKey      bool
	WallMargin         time.Duration
}

type toolFlag struct{ tools *[]toolSpec }

func (f toolFlag) String() string { return "" }

func (f toolFlag) Set(value string) error {
	name, path, ok := strings.Cut(value, "=")
	if !ok {
		return errors.New("--tool must be name=/absolute/path")
	}
	*f.tools = append(*f.tools, toolSpec{Name: name, Path: path})
	return nil
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	opt := options{RequireAPIKey: true, WallMargin: defaultWallMargin}
	flags := flag.NewFlagSet("cortex-trajectory-claude", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opt.Agent, "agent", "", "absolute path of the claude executable")
	flags.StringVar(&opt.Cortex, "cortex", "", "absolute path of the cortex executable (cortex arm)")
	flags.StringVar(&opt.CortexInstructions, "cortex-instructions", "", "absolute path of the agent-instruction snippet appended to the system prompt in the cortex arm only")
	flags.Var(toolFlag{tools: &opt.Tools}, "tool", "extra executable exposed to both arms, name=/absolute/path (repeatable)")
	flags.BoolVar(&opt.RequireAPIKey, "require-api-key", true, "block the run when ANTHROPIC_API_KEY is empty")
	flags.DurationVar(&opt.WallMargin, "wall-margin", defaultWallMargin, "time reserved out of the wall-time budget for observation and output")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("unexpected positional arguments")
	}
	if err := opt.validate(); err != nil {
		return options{}, err
	}
	return opt, nil
}

func (o options) validate() error {
	if o.Agent == "" {
		return errors.New("--agent is required")
	}
	if err := requireCleanAbs("--agent", o.Agent); err != nil {
		return err
	}
	if o.Cortex != "" {
		if err := requireCleanAbs("--cortex", o.Cortex); err != nil {
			return err
		}
	}
	if o.CortexInstructions != "" {
		if err := requireCleanAbs("--cortex-instructions", o.CortexInstructions); err != nil {
			return err
		}
	}
	if o.WallMargin < 0 {
		return errors.New("--wall-margin cannot be negative")
	}
	seen := map[string]bool{}
	for _, tool := range o.Tools {
		if !toolNamePattern.MatchString(tool.Name) {
			return fmt.Errorf("invalid --tool name %q", tool.Name)
		}
		if reservedToolNames[tool.Name] {
			return fmt.Errorf("--tool name %q is reserved", tool.Name)
		}
		if seen[tool.Name] {
			return fmt.Errorf("duplicate --tool name %q", tool.Name)
		}
		seen[tool.Name] = true
		if err := requireCleanAbs("--tool "+tool.Name, tool.Path); err != nil {
			return err
		}
	}
	return nil
}

func requireCleanAbs(label, path string) error {
	if strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s must be an absolute clean path", label)
	}
	return nil
}
