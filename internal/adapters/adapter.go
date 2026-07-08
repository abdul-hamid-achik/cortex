// Package adapters normalizes each downstream tool (codemap, vecgrep, cairn,
// glyph, fcheap, tvault, git) behind one interface so the kernel's workflow
// engine only ever sees a normalized result envelope (SPEC §11). Adapters
// validate input, apply timeouts, redact secrets, and mark whether a result is
// authoritative, partial, or unavailable — they never fabricate a missing
// tool's output.
package adapters

import "context"

// Capability is a coarse role an adapter fills (SPEC §11.2).
type Capability string

const (
	CapabilityDiscover  Capability = "discover"  // vecgrep
	CapabilityStructure Capability = "structure" // codemap, git
	CapabilityBrowser   Capability = "browser"   // cairntrace
	CapabilityTerminal  Capability = "terminal"  // glyphrun
	CapabilityArtifacts Capability = "artifacts" // fcheap
	CapabilitySecrets   Capability = "secrets"   // tvault
)

// Status reports how much trust a result carries (SPEC §11.4).
type Status string

const (
	StatusAuthoritative Status = "authoritative"
	StatusPartial       Status = "partial"
	StatusUnavailable   Status = "unavailable"
	StatusError         Status = "error"
	// StatusBlocked means the action was refused by policy before executing
	// (e.g. an external mutation with no approval; SPEC §16.2 #4).
	StatusBlocked Status = "blocked"
)

// Request is a normalized operation for an adapter to execute.
type Request struct {
	TaskID    string
	Operation string
	Input     map[string]any
}

// Str returns a string input value (empty when absent).
func (r Request) Str(key string) string {
	if v, ok := r.Input[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Int returns an int input value, or def when absent/invalid.
func (r Request) Int(key string, def int) int {
	if v, ok := r.Input[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return def
}

// StrSlice returns a []string input value (nil when absent).
func (r Request) StrSlice(key string) []string {
	v, ok := r.Input[key]
	if !ok {
		return nil
	}
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Location pins a fact to a source position (mirrors domain.Location; kept
// adapter-local so adapters don't import domain's full schema).
type Location struct {
	File      string
	StartLine int
	EndLine   int
	Symbol    string
}

// Fact is adapter-level evidence: a claim plus provenance, before the kernel
// stamps it with an ID/timestamp and promotes it to a domain.Evidence record.
type Fact struct {
	Kind       string // maps to domain.EvidenceKind
	Claim      string
	Confidence string // maps to domain.Confidence
	Location   *Location
	URI        string
	Sensitive  bool
}

// ArtifactRef is a durable-artifact pointer produced by an adapter.
type ArtifactRef struct {
	ID      string
	Kind    string
	URI     string
	Summary string
}

// Verdict is a behavioral run's structured pass/fail/errored outcome, carried
// separately from Status because a run that FAILED still executed authoritatively
// (the tool worked and reported a failure). It lets the kernel classify a run
// without string-matching warning text (SPEC §11.4). Empty for non-behavioral
// results.
type Verdict string

const (
	VerdictPassed  Verdict = "passed"
	VerdictFailed  Verdict = "failed"
	VerdictErrored Verdict = "errored" // ambiguous/infra error — NOT a behavioral failure
)

// Result is the normalized output of an adapter operation (SPEC §11.2).
type Result struct {
	Tool      string
	Operation string
	Status    Status
	Summary   string
	Facts     []Fact
	Artifacts []ArtifactRef
	Warnings  []string
	RawRef    string
	// Verdict is the behavioral pass/fail/errored outcome for browser/terminal
	// runs (empty otherwise). The kernel reads it instead of scanning Warnings.
	Verdict Verdict
	// Raw is the redacted raw tool output, retained for the case file's evidence
	// store. It is NOT returned to the model by default (SPEC §10.4).
	Raw string
}

// Adapter normalizes one downstream tool (SPEC §11.2).
type Adapter interface {
	Name() string
	Capabilities() []Capability
	Health(context.Context) error
	Execute(context.Context, Request) (Result, error)
}

// unavailable builds a degraded result for a missing/unhealthy tool. It records
// a tool_unavailable fact rather than fabricating output (SPEC §17.1).
func unavailable(tool, op, reason string) Result {
	return Result{
		Tool:      tool,
		Operation: op,
		Status:    StatusUnavailable,
		Summary:   tool + " unavailable: " + reason,
		Facts: []Fact{{
			Kind:       "tool_unavailable",
			Claim:      tool + " is unavailable (" + reason + "); results depending on it are blocked",
			Confidence: "unknown",
		}},
		Warnings: []string{tool + " unavailable: " + reason},
	}
}
