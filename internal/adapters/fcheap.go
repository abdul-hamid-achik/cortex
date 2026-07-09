package adapters

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Fcheap adapts the fcheap CLI as a durable evidence archive (SPEC §11.3,
// §12.6): stash artifacts, search across stashes, and connect a stash to the
// codebase that likely owns a bug. `--json` is a persistent flag on every
// subcommand.
type Fcheap struct{ tool }

// NewFcheap builds an fcheap adapter.
func NewFcheap() *Fcheap { return &Fcheap{tool: newTool("fcheap", 60*time.Second)} }

func (f *Fcheap) Name() string { return "fcheap" }

func (f *Fcheap) Capabilities() []Capability { return []Capability{CapabilityArtifacts} }

// Health probes fcheap via `fcheap --version`.
func (f *Fcheap) Health(ctx context.Context) error {
	if !binExists(f.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, err := f.run.run(ctx, "", f.bin, "--version")
	return err
}

// Execute routes fcheap operations.
func (f *Fcheap) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(f.bin) {
		return unavailable("fcheap", req.Operation, "not on PATH"), nil
	}
	dir := req.Str("dir")
	switch req.Operation {
	case "search":
		return f.search(ctx, dir, req.Str("query"), req.Int("limit", 10))
	case "connect":
		return f.connect(ctx, dir, req.Str("stash"), req.Str("codebase"), req.Str("query"), req.Int("limit", 10))
	case "list":
		return f.list(ctx, dir, req.StrSlice("tags"))
	case "verify":
		return f.verify(ctx, dir, req.Str("stash"))
	default:
		return Result{Tool: "fcheap", Operation: req.Operation, Status: StatusError,
			Summary: "unknown fcheap operation: " + req.Operation}, nil
	}
}

// fcSearchHit mirrors fcheap's analyze.SearchResult (the real `search`/`connect`
// match shape): {stash_id, score, text, file?, source?}. There is no line
// number and the snippet field is `text`, not `snippet`.
type fcSearchHit struct {
	StashID string  `json:"stash_id"`
	Score   float64 `json:"score"`
	Text    string  `json:"text"`
	File    string  `json:"file"`
	Source  string  `json:"source"`
}

func (f *Fcheap) search(ctx context.Context, dir, query string, limit int) (Result, error) {
	if query == "" {
		return Result{Tool: "fcheap", Operation: "search", Status: StatusError, Summary: "search needs a query"}, nil
	}
	stdout, stderr, code, err := f.exec(ctx, dir, "search", query, "--limit", strconv.Itoa(limit), "--json")
	if err != nil {
		return unavailable("fcheap", "search", err.Error()), nil
	}
	// `fcheap search --json` emits a bare array of SearchResult.
	var hits []fcSearchHit
	if derr := decodeJSON(stdout, &hits); derr != nil {
		return degraded("fcheap", "search", stdout, stderr, code), nil
	}
	facts := make([]Fact, 0, len(hits))
	for _, h := range hits {
		fact := Fact{Kind: "artifact", Confidence: "low",
			Claim: fmt.Sprintf("archived evidence in stash %s: %s", h.StashID, clip(h.Text, 100)),
			URI:   "fcheap://stash/" + h.StashID}
		if h.File != "" {
			fact.Location = &Location{File: h.File}
		}
		facts = append(facts, fact)
	}
	return Result{
		Tool: "fcheap", Operation: "search", Status: StatusAuthoritative,
		Summary: fmt.Sprintf("found %s across stashes for %q", pluralize(len(hits), "match"), clip(query, 40)),
		Facts:   facts,
		Raw:     stdout,
	}, nil
}

func (f *Fcheap) connect(ctx context.Context, dir, stash, codebase, query string, limit int) (Result, error) {
	if stash == "" || codebase == "" {
		return Result{Tool: "fcheap", Operation: "connect", Status: StatusError, Summary: "connect needs a stash and a codebase dir"}, nil
	}
	args := []string{"connect", stash, codebase, "--limit", strconv.Itoa(limit), "--json"}
	if query != "" {
		args = append(args, "--query", query)
	}
	stdout, stderr, code, err := f.exec(ctx, dir, args...)
	if err != nil {
		return unavailable("fcheap", "connect", err.Error()), nil
	}
	// `fcheap connect --json` emits an object: {stash_id, codebase, query,
	// matches:[SearchResult], index_status} — NOT a bare array. fcheap ≥0.28
	// returns exit 0 with index_status:"missing" (was exit 1) when the codebase
	// has no index; surface that honestly rather than "0 candidates".
	var out struct {
		Matches     []fcSearchHit `json:"matches"`
		IndexStatus string        `json:"index_status"`
	}
	if derr := decodeJSON(stdout, &out); derr != nil {
		return degraded("fcheap", "connect", stdout, stderr, code), nil
	}
	if out.IndexStatus == "missing" && len(out.Matches) == 0 {
		return Result{Tool: "fcheap", Operation: "connect", Status: StatusPartial,
			Summary: fmt.Sprintf("codebase %s is not indexed for connect", codebase),
			Facts: []Fact{{Kind: "artifact", Confidence: "low",
				Claim: fmt.Sprintf("codebase %s is not indexed for connect — build its index or point at an indexed repo", codebase)}},
			Raw: stdout}, nil
	}
	facts := make([]Fact, 0, len(out.Matches))
	for _, h := range out.Matches {
		claim := fmt.Sprintf("stash %s likely maps to %s (score %.2f)", stash, h.File, h.Score)
		if snip := clip(firstLine(h.Text), 80); snip != "" {
			claim += ": " + snip
		}
		facts = append(facts, Fact{Kind: "artifact", Confidence: "low",
			Claim: claim, Location: &Location{File: h.File}})
	}
	return Result{
		Tool: "fcheap", Operation: "connect", Status: StatusAuthoritative,
		Summary: fmt.Sprintf("connected stash %s to %s owning-code candidate(s)", stash, strconv.Itoa(len(out.Matches))),
		Facts:   facts,
		Raw:     stdout,
	}, nil
}

func (f *Fcheap) verify(ctx context.Context, dir, stash string) (Result, error) {
	stash = strings.TrimPrefix(stash, "fcheap://stash/")
	if stash == "" {
		return Result{Tool: "fcheap", Operation: "verify", Status: StatusError, Summary: "artifact verification needs a stash ID or fcheap:// URI"}, nil
	}
	stdout, stderr, code, err := f.exec(ctx, dir, "info", stash, "--json")
	if err != nil {
		return unavailable("fcheap", "verify", err.Error()), nil
	}
	if code != 0 {
		return Result{Tool: "fcheap", Operation: "verify", Status: StatusPartial,
			Summary:  fmt.Sprintf("stash %s could not be verified", stash),
			Warnings: []string{firstNonEmpty(firstLine(stderr), fmt.Sprintf("fcheap info exited %d", code))}, Raw: stdout}, nil
	}
	var out struct {
		ID    string `json:"id"`
		Files []any  `json:"files"`
	}
	if derr := decodeJSON(stdout, &out); derr != nil {
		return degraded("fcheap", "verify", stdout, stderr, code), nil
	}
	if out.ID == "" {
		return Result{Tool: "fcheap", Operation: "verify", Status: StatusPartial,
			Summary: "fcheap returned no stash identity", Raw: stdout}, nil
	}
	claim := fmt.Sprintf("artifact stash %s exists and its manifest is readable (%s)", out.ID, pluralize(len(out.Files), "file"))
	return Result{Tool: "fcheap", Operation: "verify", Status: StatusAuthoritative,
		Summary: claim, Facts: []Fact{{Kind: "artifact", Claim: claim, Confidence: "high", URI: "fcheap://stash/" + out.ID}},
		Artifacts: []ArtifactRef{{ID: out.ID, Kind: "fcheap", URI: "fcheap://stash/" + out.ID, Summary: claim}}, Raw: stdout}, nil
}

type fcStash struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Tool      string   `json:"tool"`
	Tags      []string `json:"tags"`
	Files     int      `json:"file_count"` // fcheap emits file_count, not files
	CreatedAt string   `json:"created_at"`
}

func (f *Fcheap) list(ctx context.Context, dir string, tags []string) (Result, error) {
	args := []string{"list", "--json"}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	stdout, stderr, code, err := f.exec(ctx, dir, args...)
	if err != nil {
		return unavailable("fcheap", "list", err.Error()), nil
	}
	var stashes []fcStash
	if derr := decodeJSON(stdout, &stashes); derr != nil {
		return degraded("fcheap", "list", stdout, stderr, code), nil
	}
	arts := make([]ArtifactRef, 0, len(stashes))
	for _, s := range stashes {
		arts = append(arts, ArtifactRef{ID: s.ID, Kind: s.Tool, URI: "fcheap://stash/" + s.ID,
			Summary: fmt.Sprintf("%s (%s)", firstNonEmpty(s.Name, s.ID), pluralize(s.Files, "file"))})
	}
	return Result{
		Tool: "fcheap", Operation: "list", Status: StatusAuthoritative,
		Summary:   pluralize(len(stashes), "stash"),
		Artifacts: arts,
		Raw:       stdout,
	}, nil
}

// Save stashes a directory/file as durable evidence and returns the stash ID.
// Used by the persist phase for high-value artifacts (SPEC §12.6). It is a
// write method, not a query op.
func (f *Fcheap) Save(ctx context.Context, dir, path string, tags []string, toolLabel string) (string, error) {
	if !binExists(f.bin) {
		return "", ErrToolMissing
	}
	args := []string{"save", path, "--json"}
	if toolLabel != "" {
		args = append(args, "--tool", toolLabel)
	}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	// Index on save (fcheap ≥0.28) so the archived evidence is immediately
	// searchable — otherwise `fcheap search` returns nothing for cortex's own
	// stashes (a silently-dead archive→search loop). A stash is a write, so no
	// auto-retry (SPEC §17.3) via execOnce.
	withIndex := append(append([]string{}, args...), "--index")
	stdout, serr, code, err := f.execOnce(ctx, dir, withIndex...)
	// An old fcheap rejects --index at flag-parse time (non-zero exit, "unknown
	// flag" on stderr — err stays nil since exit is data), before writing any
	// stash, so retry without it: archiving must still succeed, just un-indexed.
	if err != nil || (code != 0 && (containsFold(serr, "unknown flag") || containsFold(serr, "--index"))) {
		stdout, _, _, err = f.execOnce(ctx, dir, args...)
	}
	if err != nil {
		return "", err
	}
	// `fcheap save --json` emits the manifest at the top level (id, tool, tags,
	// files, …), not wrapped in a "manifest" object.
	var out struct {
		ID string `json:"id"`
	}
	if derr := decodeJSON(stdout, &out); derr != nil {
		return "", derr
	}
	return out.ID, nil
}
