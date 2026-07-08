package adapters

import (
	"context"
	"time"
)

// Cairntrace adapts the cairn CLI for browser behavior verification (SPEC §11.3,
// §12.4). `cairn run <spec> --json` executes a browser contract; the exit code
// is authoritative (0 ok, 1 outcome fail, 2 errored, 6 contract-hash mismatch).
type Cairntrace struct{ tool }

// NewCairntrace builds a cairntrace adapter.
func NewCairntrace() *Cairntrace { return &Cairntrace{tool: newTool("cairn", 180*time.Second)} }

func (c *Cairntrace) Name() string { return "cairntrace" }

func (c *Cairntrace) Capabilities() []Capability { return []Capability{CapabilityBrowser} }

// Health probes cairn via `cairn --version` — cheap and side-effect-free.
// (The old `cairn doctor --format json` probe spawned codemap/vecgrep/vidtrace/
// tvault sub-processes just to confirm the binary is present; cairn ≥1.29 has a
// real --version, so a plain version probe suffices.)
func (c *Cairntrace) Health(ctx context.Context) error {
	return c.healthByVersion(ctx)
}

// Execute routes cairntrace operations; "run" executes a browser spec.
func (c *Cairntrace) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(c.bin) {
		return unavailable("cairntrace", req.Operation, "not on PATH"), nil
	}
	switch req.Operation {
	case "run":
		return c.runSpec(ctx, req.Str("dir"), req.Str("spec"))
	default:
		return Result{Tool: "cairntrace", Operation: req.Operation, Status: StatusError,
			Summary: "unknown cairntrace operation: " + req.Operation}, nil
	}
}

// SelectSpecs returns the browser spec paths whose coversSymbol intersects the
// change's blast radius (cairn ≥1.30 `run --select-only --since-codemap <ref>`).
// It resolves selection WITHOUT launching a browser, so cortex can auto-pick the
// specs that prove a change at verify time instead of leaving a not_run receipt.
// sinceRef scopes the diff; without codemap the tool selects all expanded specs.
func (c *Cairntrace) SelectSpecs(ctx context.Context, dir, sinceRef string) ([]string, error) {
	if !binExists(c.bin) {
		return nil, ErrToolMissing
	}
	// cairn run REQUIRES a spec positional (unlike glyph affected-specs, which
	// defaults to "."). "." makes it expand the workspace's specs; --select-only
	// resolves coverage without launching a browser.
	args := []string{"run", ".", "--select-only", "--json"}
	if sinceRef != "" {
		args = append(args, "--since-codemap", sinceRef)
	}
	stdout, _, _, err := c.exec(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	var out struct {
		Selected []struct {
			Path string `json:"path"`
		} `json:"selected"`
	}
	if derr := decodeJSON(stdout, &out); derr != nil {
		return nil, derr
	}
	paths := make([]string, 0, len(out.Selected))
	for _, s := range out.Selected {
		if s.Path != "" {
			paths = append(paths, s.Path)
		}
	}
	return paths, nil
}

func (c *Cairntrace) runSpec(ctx context.Context, dir, spec string) (Result, error) {
	if spec == "" {
		return Result{Tool: "cairntrace", Operation: "run", Status: StatusError, Summary: "run needs a spec path"}, nil
	}
	stdout, stderr, code, err := c.exec(ctx, dir, "run", spec, "--json")
	if err != nil {
		return unavailable("cairntrace", "run", err.Error()), nil
	}
	return behavioralResult("cairntrace", "browser_run", spec, code, stdout, stderr), nil
}
