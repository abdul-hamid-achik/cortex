package domain

import "strings"

// Budget bounds tool use within a workflow so an agent can't thrash (SPEC §7.3).
type Budget struct {
	MaxParallelCalls          int `json:"max_parallel_calls"`
	MaxInvestigationRounds    int `json:"max_investigation_rounds"`
	MaxRawOutputBytesPerTool  int `json:"max_raw_output_bytes_per_tool"`
	MaxEvidenceItemsReturned  int `json:"max_evidence_items_returned"`
	MaxCandidateFilesReturned int `json:"max_candidate_files_returned"`
	MaxAutoRetriesPerTool     int `json:"max_auto_retries_per_tool"`
}

// DefaultBudget is the v0.1 budget from SPEC §7.3.
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

// Route is a recommended tool sequence for a question (SPEC §7.1).
type Route struct {
	First    string // primary tool
	FollowUp string // secondary tool
	Why      string
}

// RouteFor chooses a discovery/structure route from a question and surfaces
// (SPEC §7.1 routing matrix). It is deliberately rule-based, not learned — v0.1
// prefers explicit routing over telemetry-derived policy (SPEC §24 #6).
func RouteFor(question string, surfaces []Surface) Route {
	q := strings.ToLower(question)
	has := func(s Surface) bool {
		for _, x := range surfaces {
			if x == s {
				return true
			}
		}
		return false
	}

	switch {
	// Terminal is checked before browser because "tui"/"cli" questions are
	// terminal, and the browser " ui " token must not catch them.
	case has(SurfaceTerminal) || containsAny(q, "cli", "tui", "terminal", "prompt", "stdout", "command output"):
		return Route{First: "glyphrun", FollowUp: "codemap", Why: "prove terminal behavior, then map to implementation"}
	case has(SurfaceBrowser) || containsAny(q, "click", "page", "render", "redirect", " ui ", "button", "screen"):
		return Route{First: "cairntrace", FollowUp: "codemap", Why: "prove observed browser failure, then map UI evidence to code"}
	case containsAny(q, "what breaks", "impact", "blast radius", "if i change", "callers of", "affected by"):
		return Route{First: "codemap", FollowUp: "codemap", Why: "blast-radius questions are structural, not semantic"}
	case looksLikeSymbol(question):
		// SPEC §7.2 negative rule: avoid vecgrep when a known symbol resolves
		// directly via codemap. The follow-up stays structural, not semantic.
		return Route{First: "codemap", FollowUp: "codemap", Why: "a known symbol resolves directly with the code graph"}
	case containsAny(q, "video", "recording", "screen record", "footage", "bug clip"):
		return Route{First: "vidtrace", FollowUp: "codemap", Why: "turn the bug video into timestamped evidence, then resolve the owning code"}
	case containsAny(q, "artifact", "old run", "screenshot", "log bundle", "stash"):
		return Route{First: "fcheap", FollowUp: "vecgrep", Why: "recover prior evidence, then link it to code"}
	case containsAny(q, "secret", "credential", "token", "api key", "env var"):
		return Route{First: "tvault", FollowUp: "codemap", Why: "check capability without exposing values, then find readers"}
	default:
		return Route{First: "vecgrep", FollowUp: "codemap", Why: "discover by meaning, then resolve structure"}
	}
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
// (SPEC §3.6 table).
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
