package adapters

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// CommandSpec is a trusted, repository-configured verifier. It is intentionally
// an argv array: Cortex never invokes a shell, and callers choose only the
// verifier name—not executable text.
type CommandSpec struct {
	Argv    []string
	Kind    string
	Surface string
	Timeout time.Duration
}

// CommandVerifier runs named test/build/lint commands declared in cortex.yaml.
// It is one adapter regardless of how many commands are configured, keeping the
// registry and MCP surface compact.
type CommandVerifier struct {
	specs map[string]CommandSpec
	run   runner
	red   *redact.Redactor
}

func NewCommandVerifier(specs map[string]CommandSpec) *CommandVerifier {
	copySpecs := make(map[string]CommandSpec, len(specs))
	for name, spec := range specs {
		spec.Argv = append([]string(nil), spec.Argv...)
		copySpecs[name] = spec
	}
	return &CommandVerifier{specs: copySpecs, run: execRunner{}, red: redact.New()}
}

func (c *CommandVerifier) Name() string { return "command" }

func (c *CommandVerifier) Capabilities() []Capability { return []Capability{CapabilityStructure} }

// Health verifies that every configured executable is resolvable. With no
// configured commands the adapter is healthy but inert.
func (c *CommandVerifier) Health(context.Context) error {
	names := make([]string, 0, len(c.specs))
	for name := range c.specs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := c.specs[name]
		if len(spec.Argv) == 0 || !binExists(spec.Argv[0]) {
			return fmt.Errorf("command verifier %s executable is unavailable", name)
		}
	}
	return nil
}

// Execute runs an exact configured verifier by name. req.Operation is the
// configuration key; request input cannot replace or append argv.
func (c *CommandVerifier) Execute(ctx context.Context, req Request) (Result, error) {
	spec, ok := c.specs[req.Operation]
	if !ok {
		return Result{Tool: c.Name(), Operation: req.Operation, Status: StatusError,
			Summary: fmt.Sprintf("unknown configured command verifier %q", req.Operation)}, nil
	}
	if len(spec.Argv) == 0 || !binExists(spec.Argv[0]) {
		return unavailable(c.Name(), req.Operation, "configured executable is not on PATH"), nil
	}
	t := tool{bin: spec.Argv[0], run: c.run, redact: c.red, timeout: spec.Timeout}
	stdout, stderr, code, err := t.execOnce(ctx, req.Str("dir"), spec.Argv[1:]...)
	if err != nil {
		return unavailable(c.Name(), req.Operation, err.Error()), nil
	}
	command := c.red.String(strings.Join(spec.Argv, " "))
	raw := commandRaw(command, stdout, stderr, code)
	claim := fmt.Sprintf("configured %s verifier %q passed: %s", spec.Kind, req.Operation, command)
	result := Result{
		Tool: c.Name(), Operation: req.Operation, Status: StatusAuthoritative,
		Summary: claim, Verdict: VerdictPassed, Raw: raw,
		Facts: []Fact{{Kind: spec.Kind, Claim: claim, Confidence: "high"}},
	}
	if code != 0 {
		claim = fmt.Sprintf("configured %s verifier %q failed with exit %d: %s", spec.Kind, req.Operation, code, command)
		result.Summary = claim
		result.Verdict = VerdictFailed
		result.Facts[0].Claim = claim
		result.Warnings = []string{claim}
	}
	return result, nil
}

func commandRaw(command, stdout, stderr string, code int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\nexit: %d\n", command, code)
	if stdout != "" {
		b.WriteString("stdout:\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
	}
	if stderr != "" {
		b.WriteString("stderr:\n")
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
