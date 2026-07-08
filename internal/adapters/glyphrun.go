package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Glyphrun adapts the glyph CLI for terminal/TUI behavior verification
// (SPEC §11.3, §12.5). glyph uses `--format json` (not `--json`) and that flag
// must precede subcommand flags; `glyph run <spec> --format json` executes a
// terminal contract.
type Glyphrun struct{ tool }

// NewGlyphrun builds a glyphrun adapter.
func NewGlyphrun() *Glyphrun { return &Glyphrun{tool: newTool("glyph", 120*time.Second)} }

func (g *Glyphrun) Name() string { return "glyphrun" }

func (g *Glyphrun) Capabilities() []Capability { return []Capability{CapabilityTerminal} }

// Health probes glyph via `glyph --version`.
func (g *Glyphrun) Health(ctx context.Context) error {
	if !binExists(g.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, err := g.run.run(ctx, "", g.bin, "--version")
	return err
}

// Execute routes glyphrun operations; "run" executes a terminal spec.
func (g *Glyphrun) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(g.bin) {
		return unavailable("glyphrun", req.Operation, "not on PATH"), nil
	}
	switch req.Operation {
	case "run":
		return g.runSpec(ctx, req.Str("dir"), req.Str("spec"))
	default:
		return Result{Tool: "glyphrun", Operation: req.Operation, Status: StatusError,
			Summary: "unknown glyphrun operation: " + req.Operation}, nil
	}
}

// AffectedSpecs returns the terminal spec paths whose coversSymbol intersects
// the change's blast radius (glyph ≥v0.9 `affected-specs --format json`). It is a
// read-only selection used at verify time to auto-pick which specs prove a
// change, so cortex doesn't leave a not_run receipt when a covering spec exists.
// sinceRef scopes the diff (empty = whole working tree). Requires codemap
// indexed; degrades to an empty selection otherwise.
func (g *Glyphrun) AffectedSpecs(ctx context.Context, dir, sinceRef string) ([]string, error) {
	if !binExists(g.bin) {
		return nil, ErrToolMissing
	}
	args := []string{"affected-specs", "--format", "json"}
	if sinceRef != "" {
		args = append(args, "--since", sinceRef)
	}
	stdout, _, _, err := g.exec(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	var out struct {
		Specs []struct {
			Path string `json:"path"`
		} `json:"specs"`
	}
	if derr := decodeJSON(stdout, &out); derr != nil {
		return nil, derr
	}
	paths := make([]string, 0, len(out.Specs))
	for _, s := range out.Specs {
		if s.Path != "" {
			paths = append(paths, s.Path)
		}
	}
	return paths, nil
}

func (g *Glyphrun) runSpec(ctx context.Context, dir, spec string) (Result, error) {
	if spec == "" {
		return Result{Tool: "glyphrun", Operation: "run", Status: StatusError, Summary: "run needs a spec path"}, nil
	}
	// --format precedes the spec path (glyph persistent-flag ordering rule).
	stdout, stderr, code, err := g.exec(ctx, dir, "run", spec, "--format", "json")
	if err != nil {
		return unavailable("glyphrun", "run", err.Error()), nil
	}
	return behavioralResult("glyphrun", "terminal_run", spec, code, stdout, stderr), nil
}

// runResult is the shared shape of a glyph/cairn spec run (the fields we use).
// The two tools differ in WHERE per-item failure detail lives: glyph puts a
// human `message` on each outcome; cairn's OutcomeResult v1 is {id,status} with
// NO message (it's `.strict()`), and the failure reason lives on steps[].error.
// We read both so failure detail survives on either tool.
type runResult struct {
	Status   string `json:"status"` // passed | failed | errored
	RunDir   string `json:"runDir"`
	ExitCode int    `json:"exitCode"`
	Outcomes []struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Message string `json:"message"` // glyph carries this; cairn does not
	} `json:"outcomes"`
	Steps []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  string `json:"error"` // cairn carries the failure reason here
	} `json:"steps"`
	// glyph ≥v0.9 classifies an errored/failed run: errorKind names the cause and
	// diagnostic is the human reason; a contract-hash mismatch also carries the
	// computed vs stamped hashes. Absent on older glyph — the fields stay "".
	ErrorKind    string `json:"errorKind"`
	Diagnostic   string `json:"diagnostic"`
	ContractHash string `json:"contractHash"`
	ExpectedHash string `json:"expectedHash"`
	// cairn ≥1.30 gives the canonical "why" in one place: `summary` is an
	// always-populated one-liner and `failure` (on failed|errored) is the first
	// failed step/outcome with its message — so we don't scan steps[]/outcomes[].
	// `spec.contractHash` is now always present (stamped or computed on the fly).
	Summary string `json:"summary"`
	Failure *struct {
		Outcome string `json:"outcome"`
		Step    string `json:"step"`
		Message string `json:"message"`
	} `json:"failure"`
	Spec struct {
		ContractHash string `json:"contractHash"`
	} `json:"spec"`
}

// Behavioral outcome markers embedded in a result's warnings so the kernel can
// distinguish a genuine behavioral failure from an ambiguous errored run
// (SPEC §11.4: MUST NOT convert ambiguous output into a high-confidence
// conclusion). "errored" covers infrastructure/spec/cold-start/hash-mismatch
// exits — none of which is a clean behavioral verdict.
const (
	markFailed  = "verification did NOT pass"
	markErrored = "run ERRORED (ambiguous"
)

// behavioralResult maps a spec run into a normalized verification result shared
// by the browser (cairn) and terminal (glyph) verifiers. It classifies the run
// three ways — passed, failed, or errored — from the JSON status when present,
// else the exit code. A definitive pass/fail is high confidence; an errored run
// is only medium confidence and is NOT reported as a behavioral failure.
func behavioralResult(toolName, kind, spec string, code int, stdout, stderr string) Result {
	var rr runResult
	parseErr := decodeJSON(stdout, &rr) // best-effort enrichment; exit code is a fallback

	// If the tool emitted a NON-EMPTY payload we could not parse and it carried
	// no explicit status, the verdict is unreadable — a bare exit 0 must not be
	// laundered into a confident PASS (a verifier that "passes by default" would
	// mask a real failure). Treat unreadable output as errored/inconclusive,
	// never passed. An EMPTY payload still falls back to the exit code so old
	// binaries that only signal via exit status keep working.
	unreadable := parseErr != nil && strings.TrimSpace(stdout) != "" && rr.Status == ""

	outcome := classifyRun(rr.Status, code) // "passed" | "failed" | "errored"
	if unreadable {
		outcome = "errored"
	}
	conf := "high"
	if outcome == "errored" {
		conf = "medium"
	}

	claim := fmt.Sprintf("%s spec %s %s", toolName, spec, strings.ToUpper(outcome))
	facts := []Fact{{Kind: kind, Claim: claim, Confidence: conf, URI: rr.RunDir}}
	// The canonical failure reason (cairn ≥1.30) is the single authoritative
	// "why", so we prefer it over scanning outcomes[]/steps[]. Its absence means
	// an older tool, so the per-item fallbacks below still apply.
	canonical := ""
	if rr.Failure != nil {
		canonical = rr.Failure.Message
	}
	switch {
	case canonical != "":
		facts = append(facts, Fact{Kind: kind, Confidence: conf, Claim: clip(canonical, 200)})
	default:
		for _, o := range rr.Outcomes {
			if o.Status != "pass" && o.Status != "passed" && o.Message != "" {
				facts = append(facts, Fact{Kind: kind, Confidence: conf,
					Claim: fmt.Sprintf("outcome %s: %s", o.ID, clip(o.Message, 120))})
			}
		}
		// Failed-step errors carry the failure detail on older cairn (whose
		// outcomes have no message), so a browser failure explains itself.
		for _, s := range rr.Steps {
			if s.Status != "passed" && s.Status != "skipped" && s.Error != "" {
				facts = append(facts, Fact{Kind: kind, Confidence: conf,
					Claim: fmt.Sprintf("step %s failed: %s", s.ID, clip(s.Error, 120))})
			}
		}
		// A classified error reason (glyph ≥v0.9) becomes evidence.
		if outcome == "errored" && rr.Diagnostic != "" {
			facts = append(facts, Fact{Kind: kind, Confidence: conf,
				Claim: firstNonEmpty(rr.ErrorKind, "errored") + ": " + clip(rr.Diagnostic, 160)})
		}
	}
	var warns []string
	switch outcome {
	case "failed":
		detail := ""
		if canonical != "" {
			detail = ": " + clip(canonical, 140)
		}
		warns = append(warns, fmt.Sprintf("%s %s%s (exit %d)", toolName, markFailed, detail, code))
	case "errored":
		// Keep the markErrored substring so the kernel still maps this to
		// inconclusive (SPEC §11.4), but make the specific cause actionable.
		switch rr.ErrorKind {
		case "contract_hash_mismatch":
			warns = append(warns, fmt.Sprintf("%s — the behavior contract changed: stamped %s != computed %s; re-stamp with `glyph spec verify <spec> --stamp` (do NOT treat as a behavioral failure; exit %d)",
				markErrored, clip(rr.ExpectedHash, 24), clip(rr.ContractHash, 24), code))
		case "spec_parse":
			warns = append(warns, fmt.Sprintf("%s — the terminal spec is malformed/schema-invalid: %s; fix the spec (exit %d)", markErrored, clip(rr.Diagnostic, 120), code))
		default:
			detail := ""
			if d := firstNonEmpty(canonical, rr.Diagnostic); d != "" {
				detail = " — " + clip(d, 120)
			}
			warns = append(warns, fmt.Sprintf("%s%s — infrastructure/spec error, not a behavioral verdict; exit %d)", markErrored, detail, code))
		}
	}
	if outcome != "passed" && stderr != "" {
		warns = append(warns, clip(stderr, 160))
	}
	arts := []ArtifactRef{}
	if rr.RunDir != "" {
		// Record the contract hash (cairn ≥1.30 always populates it) so the run
		// bundle carries a stable "verified against contract sha256:…" identity.
		sum := toolName + " run of " + spec
		if rr.Spec.ContractHash != "" {
			sum += " (contract " + clip(rr.Spec.ContractHash, 24) + ")"
		}
		arts = append(arts, ArtifactRef{Kind: "run_bundle", URI: rr.RunDir, Summary: sum})
	}
	return Result{
		Tool: toolName, Operation: "run", Status: StatusAuthoritative,
		Summary:   claim,
		Facts:     facts,
		Artifacts: arts,
		Warnings:  warns,
		Verdict:   Verdict(outcome), // structured pass/fail/errored (SPEC §11.4)
		Raw:       stdout,
	}
}

// classifyRun distinguishes a clean pass/fail from an ambiguous errored run.
// The tool's JSON status wins; otherwise exit 0 = passed, exit 1 = failed
// (a behavioral outcome failure for both glyph and cairn), and any other exit
// is errored (cairn 2=errored, 3=cold-start, 4=lint, 5=heal, 6=hash-mismatch;
// glyph reports "errored" in JSON).
func classifyRun(status string, code int) string {
	switch status {
	case "passed":
		return "passed"
	case "failed":
		return "failed"
	case "errored":
		return "errored"
	}
	switch code {
	case 0:
		return "passed"
	case 1:
		return "failed"
	default:
		return "errored"
	}
}
