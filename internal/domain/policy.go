package domain

import "strings"

// Budget bounds tool use within a workflow so an agent can't thrash.
type Budget struct {
	MaxParallelCalls          int `json:"max_parallel_calls"`
	MaxInvestigationRounds    int `json:"max_investigation_rounds"`
	MaxRawOutputBytesPerTool  int `json:"max_raw_output_bytes_per_tool"`
	MaxEvidenceItemsReturned  int `json:"max_evidence_items_returned"`
	MaxCandidateFilesReturned int `json:"max_candidate_files_returned"`
	MaxAutoRetriesPerTool     int `json:"max_auto_retries_per_tool"`
}

// DefaultBudget is the default investigation budget.
func DefaultBudget() Budget {
	return Budget{
		MaxParallelCalls:          3,
		MaxInvestigationRounds:    3,
		MaxRawOutputBytesPerTool:  32768,
		MaxEvidenceItemsReturned:  12,
		MaxCandidateFilesReturned: 8,
		MaxAutoRetriesPerTool:     1,
	}
}

// Route is a recommended tool sequence for a question.
type Route struct {
	First    string // primary tool
	FollowUp string // secondary tool
	Why      string
}

// RoutingRule is one ordered, executable row in the routing matrix. Match is a
// concise human-readable predicate; the structured fields are what RouteFor
// evaluates. The first matching row wins.
type RoutingRule struct {
	Match    string    `json:"match"`
	Surfaces []Surface `json:"surfaces,omitempty"`
	Keywords []string  `json:"keywords,omitempty"`
	Symbol   bool      `json:"symbol,omitempty"`
	Default  bool      `json:"default,omitempty"`
	First    string    `json:"first"`
	FollowUp string    `json:"followUp"`
	Why      string    `json:"why"`
}

// RoutingMatrix is the ordered routing-policy table. It is both the
// executable source for RouteFor and the serializable source for `cortex route
// --json`; keep precedence encoded by row order.
var RoutingMatrix = []RoutingRule{
	{
		Match: "terminal surface or terminal/CLI behavior", Surfaces: []Surface{SurfaceTerminal},
		Keywords: []string{"cli", "tui", "terminal", "prompt", "stdout", "command output"},
		First:    "glyphrun", FollowUp: "codemap", Why: "prove terminal behavior, then map to implementation",
	},
	{
		Match: "browser surface or browser/UI behavior", Surfaces: []Surface{SurfaceBrowser},
		Keywords: []string{"click", "page", "render", "redirect", " ui ", "button", "screen"},
		First:    "cairntrace", FollowUp: "codemap", Why: "prove observed browser failure, then map UI evidence to code",
	},
	{
		Match:    "blast-radius or impact question",
		Keywords: []string{"what breaks", "impact", "blast radius", "if i change", "callers of", "affected by"},
		First:    "codemap", FollowUp: "codemap", Why: "blast-radius questions are structural, not semantic",
	},
	{
		Match: "known symbol", Symbol: true,
		First: "codemap", FollowUp: "codemap", Why: "a known symbol resolves directly with the code graph",
	},
	{
		Match:    "video or screen recording",
		Keywords: []string{"video", "recording", "screen record", "footage", "bug clip"},
		First:    "vidtrace", FollowUp: "codemap", Why: "turn the bug video into timestamped evidence, then resolve the owning code",
	},
	{
		Match:    "artifact or prior run bundle",
		Surfaces: []Surface{SurfaceArtifact},
		Keywords: []string{"artifact", "old run", "screenshot", "log bundle", "stash"},
		First:    "fcheap", FollowUp: "vecgrep", Why: "recover prior evidence, then link it to code",
	},
	{
		Match:    "secret or credential capability",
		Surfaces: []Surface{SurfaceSecret},
		Keywords: []string{"secret", "credential", "token", "api key", "env var"},
		First:    "tvault", FollowUp: "codemap", Why: "check capability without exposing values, then find readers",
	},
	{
		Match: "semantic discovery default", Default: true,
		First: "vecgrep", FollowUp: "codemap", Why: "discover by meaning, then resolve structure",
	},
}

// RouteFor chooses a discovery/structure route from a question and surfaces
// routing matrix. It is deliberately rule-based, not learned: the initial
// implementation prefers explicit routing over telemetry-derived policy.
func RouteFor(question string, surfaces []Surface) Route {
	q := strings.ToLower(question)
	for _, rule := range RoutingMatrix {
		if rule.matches(question, q, surfaces) {
			return Route{First: rule.First, FollowUp: rule.FollowUp, Why: rule.Why}
		}
	}
	return Route{}
}

func (r RoutingRule) matches(question, lowerQuestion string, surfaces []Surface) bool {
	for _, want := range r.Surfaces {
		for _, got := range surfaces {
			if got == want {
				return true
			}
		}
	}
	if r.Symbol && looksLikeSymbol(question) {
		return true
	}
	if containsAny(lowerQuestion, r.Keywords...) {
		return true
	}
	return r.Default
}

// looksLikeSymbol heuristically detects a known identifier (CamelCase, dotted,
// or path-like) versus a vague natural-language description.
func looksLikeSymbol(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" || strings.Contains(q, " ") {
		// A single token with structure is a symbol; multi-word is a description.
		return !strings.Contains(q, " ") && hasSymbolShape(q)
	}
	return hasSymbolShape(q)
}

func hasSymbolShape(q string) bool {
	return strings.ContainsAny(q, ".:/") || hasUpperInside(q)
}

// hasUpperInside reports an internal capital (CamelCase) — a strong symbol hint.
func hasUpperInside(q string) bool {
	for i, r := range q {
		if i > 0 && r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// SurfaceVerifier maps a verification surface to its primary verifier tool
// for the affected surface.
func SurfaceVerifier(s Surface) string {
	switch s {
	case SurfaceBrowser:
		return "cairntrace"
	case SurfaceTerminal:
		return "glyphrun"
	case SurfaceArtifact:
		return "fcheap"
	case SurfaceSecret:
		return "tvault"
	default:
		return "codemap"
	}
}
