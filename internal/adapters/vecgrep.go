package adapters

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Vecgrep adapts the vecgrep CLI for semantic/keyword discovery and cross-
// session memory. Unlike codemap it uses `-f json` (an enum
// flag, not a boolean) and `-n` for the limit. vecgrep has no `doctor`
// subcommand — health is probed via `vecgrep --version`.
type Vecgrep struct{ tool }

// NewVecgrep builds a vecgrep adapter with the 15-second code-search budget.
func NewVecgrep() *Vecgrep { return &Vecgrep{tool: newTool("vecgrep", 15*time.Second)} }

func (v *Vecgrep) Name() string { return "vecgrep" }

func (v *Vecgrep) Capabilities() []Capability { return []Capability{CapabilityDiscover} }

// Health probes vecgrep via `vecgrep --version` (no doctor subcommand exists).
func (v *Vecgrep) Health(ctx context.Context) error {
	if !binExists(v.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, err := v.run.run(ctx, "", v.bin, "--version")
	return err
}

// Execute routes vecgrep operations.
func (v *Vecgrep) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(v.bin) {
		return unavailable("vecgrep", req.Operation, "not on PATH"), nil
	}
	dir := req.Str("dir")
	switch req.Operation {
	case "search":
		return v.search(ctx, dir, req.Str("query"), firstNonEmpty(req.Str("mode"), "hybrid"), req.Int("limit", 10))
	case "similar":
		return v.similar(ctx, dir, req.Str("target"), req.Int("limit", 10))
	case "memory_recall":
		return v.memoryRecall(ctx, dir, req.Str("query"), req.StrSlice("tags"), req.Int("limit", 5))
	default:
		return Result{Tool: "vecgrep", Operation: req.Operation, Status: StatusError,
			Summary: "unknown vecgrep operation: " + req.Operation}, nil
	}
}

// vgHit is one element of vecgrep's search/similar output (bare array, or the
// hits[] of a json-envelope).
type vgHit struct {
	ChunkID    int     `json:"chunk_id"`
	FilePath   string  `json:"file_path"`
	RelPath    string  `json:"relative_path"`
	Content    string  `json:"content"`
	StartLine  int     `json:"start_line"`
	EndLine    int     `json:"end_line"`
	ChunkType  string  `json:"chunk_type"`
	SymbolName string  `json:"symbol_name"`
	Language   string  `json:"language"`
	Score      float64 `json:"score"`
}

// vgEnvelope is vecgrep ≥2.15's `-f json-envelope` shape: an index-status header
// plus hits. It lets cortex tell "indexed, nothing matched" (authoritative empty)
// apart from "no index in this workspace" (unavailable — not a false negative).
type vgEnvelope struct {
	// SchemaVersion is 0 for vecgrep's transitional pre-v1 envelope. Cortex
	// dual-reads that shape during the compatibility window and fails safely on
	// unknown explicit majors.
	SchemaVersion int `json:"schema_version,omitempty"`
	Index         *struct {
		Indexed bool `json:"indexed"`
		Fresh   bool `json:"fresh"`
		Chunks  int  `json:"chunks"`
	} `json:"index"`
	Hits []vgHit `json:"hits"`
}

func (v *Vecgrep) search(ctx context.Context, dir, query, mode string, limit int) (Result, error) {
	if query == "" {
		return Result{Tool: "vecgrep", Operation: "search", Status: StatusError, Summary: "search needs a query"}, nil
	}
	stdout, stderr, code, err := v.exec(ctx, dir, "search", query, "-m", mode, "-n", strconv.Itoa(limit), "-f", "json-envelope")
	if err != nil {
		return unavailable("vecgrep", "search", err.Error()), nil
	}
	// New binary: an index-status envelope distinguishes an absent index from an
	// empty match set, so cortex never reads "no index" as "no such code".
	var env vgEnvelope
	if decodeJSON(stdout, &env) == nil && env.Index != nil {
		if env.SchemaVersion != 0 && env.SchemaVersion != 1 {
			return degraded("vecgrep", "search", stdout, "unsupported vecgrep search schema version "+strconv.Itoa(env.SchemaVersion), code), nil
		}
		if !env.Index.Indexed {
			return vecgrepNoIndex(query), nil
		}
		// The envelope parsed as indexed, but a non-zero exit means vecgrep still
		// failed the search (e.g. "embedding profile is missing for an existing
		// index") — it can emit an indexed-but-empty envelope in that state.
		// Returning those hits as authoritative would be a confident false
		// all-clear (dogfooding 2026-07-07). Downgrade instead; mirrors the guard
		// memory_recall already applies.
		if code != 0 {
			return degraded("vecgrep", "search", stdout, stderr, code), nil
		}
		return v.hitsResult("search", query, mode, env.Hits, stdout), nil
	}
	// A "not in a vecgrep project" error (whichever stream it lands on) is the
	// same honest signal.
	if containsFold(stdout, "not in a vecgrep project") || containsFold(stderr, "not in a vecgrep project") {
		return vecgrepNoIndex(query), nil
	}
	// Old binary that doesn't emit the envelope: fall back to the bare-array
	// `-f json` shape so it still returns hits.
	so, se, code2, err2 := v.exec(ctx, dir, "search", query, "-m", mode, "-n", strconv.Itoa(limit), "-f", "json")
	if err2 != nil {
		return unavailable("vecgrep", "search", err2.Error()), nil
	}
	// The old binary reports "not in a vecgrep project" on the fallback call too;
	// keep it an honest "no index" signal rather than a degraded parse failure.
	if containsFold(so, "not in a vecgrep project") || containsFold(se, "not in a vecgrep project") {
		return vecgrepNoIndex(query), nil
	}
	var hits []vgHit
	if derr := decodeJSON(so, &hits); derr != nil {
		return degraded("vecgrep", "search", firstNonEmpty(so, stdout), firstNonEmpty(se, stderr), firstNonZero(code2, code)), nil
	}
	// A parseable array from a non-zero exit is still a failed search — don't
	// launder it into an authoritative empty/partial result (dogfooding 2026-07-07).
	if code2 != 0 {
		return degraded("vecgrep", "search", so, se, code2), nil
	}
	return v.hitsResult("search", query, mode, hits, so), nil
}

// vecgrepNoIndex reports that semantic discovery is unavailable because the
// workspace has no vecgrep index — an actionable, non-fabricating signal that a
// silent empty result would hide.
func vecgrepNoIndex(query string) Result {
	msg := "vecgrep semantic discovery unavailable: no index in this workspace (run `vecgrep init && vecgrep index`)"
	return Result{
		Tool: "vecgrep", Operation: "search", Status: StatusUnavailable,
		Summary:  msg,
		Facts:    []Fact{{Kind: "tool_unavailable", Confidence: "unknown", Claim: msg}},
		Warnings: []string{msg},
	}
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func (v *Vecgrep) similar(ctx context.Context, dir, target string, limit int) (Result, error) {
	if target == "" {
		return Result{Tool: "vecgrep", Operation: "similar", Status: StatusError, Summary: "similar needs a target"}, nil
	}
	stdout, stderr, code, err := v.exec(ctx, dir, "similar", target, "-n", strconv.Itoa(limit), "-f", "json")
	if err != nil {
		return unavailable("vecgrep", "similar", err.Error()), nil
	}
	var hits []vgHit
	if derr := decodeJSON(stdout, &hits); derr != nil {
		return degraded("vecgrep", "similar", stdout, stderr, code), nil
	}
	// A non-zero exit means the neighbor search itself failed — never report its
	// (possibly empty) hits as authoritative (dogfooding 2026-07-07).
	if code != 0 {
		return degraded("vecgrep", "similar", stdout, stderr, code), nil
	}
	return v.hitsResult("similar", target, "similarity", hits, stdout), nil
}

// minUsefulScore is the discovery usefulness floor. When every scored hit of a
// search falls below it, the search surfaced only noise (dogfooding 2026-07-15:
// 16 hits at 0.01–0.02 — doc headers and bare imports — were recorded as
// evidence and polluted the task ledger). Hits with score 0 are treated as
// unscored (old binaries / keyword mode) and never gated on score.
const minUsefulScore = 0.10

func (v *Vecgrep) hitsResult(op, q, mode string, hits []vgHit, raw string) Result {
	// Quality gate 1: drop chunks that carry no evidentiary weight — markdown
	// headings, bare import statements, punctuation-only fragments. A claim
	// like "# Cartographer" is not a fact (dogfooding 2026-07-15). When the
	// question itself is about imports/includes/requires, import lines ARE the
	// evidence and are kept.
	keepImports := queryWantsImports(q)
	kept := make([]vgHit, 0, len(hits))
	dropped := 0
	for _, h := range hits {
		if lowValueChunk(h.Content, markdownDoc(h), keepImports) {
			dropped++
			continue
		}
		kept = append(kept, h)
	}
	var warnings []string
	if dropped > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"vecgrep %s: filtered %s (heading-only, import-only, or trivial chunks are not recorded as evidence)",
			op, pluralize(dropped, "low-value hit")))
	}
	// Quality gate 2: when EVERY remaining scored hit is below the usefulness
	// floor, say so honestly instead of recording a pile of weak candidates.
	if len(kept) > 0 && allBelowScore(kept, minUsefulScore) {
		msg := fmt.Sprintf(
			"vecgrep %s: discovery returned no strong candidates for %q — all %d hits scored below %.2f; treat this as nothing found, not as evidence",
			op, clip(q, 40), len(kept), minUsefulScore)
		return Result{
			Tool: "vecgrep", Operation: op, Status: StatusAuthoritative,
			Summary:  fmt.Sprintf("%s (%s): no strong candidates for %q (%d weak hits below score %.2f)", op, mode, clip(q, 40), len(kept), minUsefulScore),
			Warnings: append(warnings, msg),
			Raw:      raw,
		}
	}
	facts := make([]Fact, 0, len(kept))
	for _, h := range kept {
		path := firstNonEmpty(h.RelPath, h.FilePath)
		claim := fmt.Sprintf("%s in %s (score %.2f)", firstNonEmpty(h.SymbolName, h.ChunkType), path, h.Score)
		if snip := clip(snippetLine(h.Content, markdownDoc(h)), 80); snip != "" {
			claim += ": " + snip
		}
		facts = append(facts, Fact{
			Kind: "semantic_search", Confidence: "low", // discovery is a candidate, not proof
			Claim:    claim,
			Location: &Location{File: path, StartLine: h.StartLine, EndLine: h.EndLine, Symbol: h.SymbolName},
		})
	}
	return Result{
		Tool: "vecgrep", Operation: op, Status: StatusAuthoritative,
		Summary:  fmt.Sprintf("%s (%s): %s for %q", op, mode, pluralize(len(facts), "candidate"), clip(q, 40)),
		Facts:    facts,
		Warnings: warnings,
		Raw:      raw,
	}
}

// snippetLine picks the line a fact's claim displays: the chunk's first
// substantive line. A chunk that opens with a heading is legitimately kept as
// evidence when a body follows — but its claim must show that body, not the
// heading. "generic in SECURITY.md (score 0.53): # Security" reads as noise
// while the chunk actually spans 24 substantive lines (dogfooding 2026-07-16).
// Falls back to the first non-empty line when nothing more substantive exists.
func snippetLine(content string, markdown bool) string {
	first := ""
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if first == "" {
			first = t
		}
		if (markdown && headingLine(t)) || importLine(t) || trivialLine(t) {
			continue
		}
		return t
	}
	return first
}

// allBelowScore reports whether every hit carries a positive score below the
// floor. Any unscored hit (score ≤ 0 — old binaries, keyword mode) disables
// the gate: absence of a score is not evidence of weakness.
func allBelowScore(hits []vgHit, floor float64) bool {
	if len(hits) == 0 {
		return false
	}
	for _, h := range hits {
		if h.Score <= 0 || h.Score >= floor {
			return false
		}
	}
	return true
}

// lowValueChunk reports whether a hit's matched content carries no evidentiary
// weight: every non-empty line is a markdown heading (markdown documents
// only), a bare import/include statement (unless the question is about
// imports), or near-empty punctuation. Empty content is NOT low value — old
// binaries omit the content field entirely, and absence of content must not
// suppress an otherwise locatable hit.
func lowValueChunk(content string, markdown, keepImports bool) bool {
	c := strings.TrimSpace(content)
	if c == "" {
		return false
	}
	for _, ln := range strings.Split(c, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if markdown && headingLine(t) {
			continue
		}
		if !keepImports && importLine(t) {
			continue
		}
		if trivialLine(t) {
			continue
		}
		return false // at least one substantive line
	}
	return true
}

// markdownDoc reports whether the hit comes from a markdown document — the
// only place a leading '#' marks a heading. Everywhere else '#' opens a
// comment (shell, Python, YAML) or a preprocessor directive (C/C++), which is
// substantive content. Unknown origin is NOT markdown: dropping evidence
// requires positive proof of noise.
func markdownDoc(h vgHit) bool {
	if strings.EqualFold(strings.TrimSpace(h.Language), "markdown") {
		return true
	}
	p := strings.ToLower(firstNonEmpty(h.RelPath, h.FilePath))
	for _, ext := range []string{".md", ".mdx", ".markdown", ".mdown"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// queryWantsImports reports whether the search question is itself about
// dependency wiring — then import/include/require lines are the evidence the
// caller asked for and must not be filtered as noise.
func queryWantsImports(q string) bool {
	for _, kw := range []string{"import", "include", "require", "depend"} {
		if containsFold(q, kw) {
			return true
		}
	}
	return false
}

// headingLine matches markdown ATX headings ("# Title") and setext underlines
// ("----", "====").
func headingLine(t string) bool {
	if strings.HasPrefix(t, "#") {
		return true
	}
	return len(t) >= 3 && strings.Trim(t, "=-") == ""
}

// importPrefixes are line starts that unambiguously mark a bare dependency
// statement (TS/JS, Go, Python, C/C++). Keyword prefixes that double as
// English prose ("use", "using", "package") are handled shape-aware in
// importLine instead.
var importPrefixes = []string{
	"import ", "import{", "import(", "export {", "export{",
	"#include", "require(",
}

// importLine matches a line that is only an import/include/require statement.
func importLine(t string) bool {
	for _, p := range importPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	// Rust "use std::fmt;", C# "using System.Text;", Go/Java "package main" /
	// "package com.example;" — only in statement shape (semicolon-terminated
	// or a single module token). Prose like "use the campaign dialog to plan a
	// probe run" must survive.
	for _, kw := range []string{"use ", "using ", "package "} {
		if strings.HasPrefix(t, kw) {
			rest := strings.TrimSpace(t[len(kw):])
			return strings.HasSuffix(rest, ";") || len(strings.Fields(rest)) == 1
		}
	}
	// JS/TS continuation (`} from "..."`) and Python (`from x import y`).
	if strings.Contains(t, ` from "`) || strings.Contains(t, " from '") {
		return true
	}
	return strings.HasPrefix(t, "from ") && strings.Contains(t, " import ")
}

// trivialLine matches punctuation-only fragments (closing braces, rules,
// delimiters) with fewer than three letters or digits.
func trivialLine(t string) bool {
	n := 0
	for _, r := range t {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			n++
			if n >= 3 {
				return false
			}
		}
	}
	return true
}

// vgMemory is one recalled memory (bare-array output).
type vgMemory struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Importance float64  `json:"importance"`
	Tags       []string `json:"tags"`
	Score      float64  `json:"score"`
}

func (v *Vecgrep) memoryRecall(ctx context.Context, dir, query string, tags []string, limit int) (Result, error) {
	if query == "" {
		return Result{Tool: "vecgrep", Operation: "memory_recall", Status: StatusError, Summary: "recall needs a query"}, nil
	}
	args := []string{"memory", "recall", query, "--limit", strconv.Itoa(limit), "--format", "json"}
	// Scope recall to the case's tags (AND-match) so prior conclusions come from
	// this project, not blended across every repo cortex has ever touched.
	if len(tags) > 0 {
		args = append(args, "--tags", joinComma(tags))
	}
	stdout, stderr, code, err := v.exec(ctx, dir, args...)
	if err != nil {
		return unavailable("vecgrep", "memory_recall", err.Error()), nil
	}
	// vecgrep ≥2.15 signals a down embedding provider with exit 3 +
	// {"error":"provider_unavailable"} on stderr (empty stdout). Classify that as
	// unavailable so recall (once wired into investigate) distinguishes "no prior
	// memory" (authoritative []) from "recall couldn't run" — never a fabricated
	// empty. Old binaries never emit this, so they fall through unchanged.
	if code == 3 || containsFold(stderr, "provider_unavailable") {
		return unavailable("vecgrep", "memory_recall", "embedding provider unavailable"), nil
	}
	var mems []vgMemory
	if derr := decodeJSON(stdout, &mems); derr != nil {
		return degraded("vecgrep", "memory_recall", stdout, stderr, code), nil
	}
	facts := make([]Fact, 0, len(mems))
	for _, m := range mems {
		facts = append(facts, Fact{Kind: "model_inference", Confidence: "low",
			Claim: fmt.Sprintf("prior memory: %s", clip(m.Content, 160))})
	}
	return Result{
		Tool: "vecgrep", Operation: "memory_recall", Status: StatusAuthoritative,
		Summary: fmt.Sprintf("recalled %s for %q", pluralize(len(mems), "memory"), clip(query, 40)),
		Facts:   facts,
		Raw:     stdout,
	}, nil
}

// Remember stores a durable memory used by the persist phase. It is
// a direct method rather than an Execute op because it is a write, not a query.
func (v *Vecgrep) Remember(ctx context.Context, dir, content string, tags []string, importance float64) error {
	if !binExists(v.bin) {
		return ErrToolMissing
	}
	args := []string{"memory", "remember", content, "--importance", strconv.FormatFloat(importance, 'f', 2, 64)}
	if len(tags) > 0 {
		args = append(args, "--tags", joinComma(tags))
	}
	// Storing a memory is a write — no automatic retry.
	_, _, _, err := v.execOnce(ctx, dir, args...)
	return err
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}
