package adapters

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	if err := ValidateArtifactID(stash); err != nil {
		return Result{Tool: "fcheap", Operation: "verify", Status: StatusError, Summary: "invalid artifact stash id: " + err.Error()}, nil
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
	if err := ValidateArtifactID(out.ID); err != nil {
		return Result{Tool: "fcheap", Operation: "verify", Status: StatusPartial,
			Summary: "fcheap returned an invalid stash identity", Raw: stdout}, nil
	}
	if out.ID != stash {
		return Result{Tool: "fcheap", Operation: "verify", Status: StatusPartial,
			Summary: "fcheap returned a different stash identity", Raw: stdout}, nil
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
	if err := ValidateArtifactID(out.ID); err != nil {
		return "", fmt.Errorf("fcheap returned invalid stash id: %w", err)
	}
	return out.ID, nil
}

// PreviewFile is one regular file in a restored stash manifest.
type PreviewFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ArtifactPreview is a bounded, redacted view of durable evidence. Binary
// content is returned as base64 only after explicit caller opt-in. Files are
// restored only into a fresh temp directory removed before this method returns.
type ArtifactPreview struct {
	StashID   string        `json:"stashId"`
	Files     []PreviewFile `json:"files"`
	Selected  string        `json:"selected,omitempty"`
	Content   string        `json:"content,omitempty"`
	Encoding  string        `json:"encoding,omitempty"` // text | base64
	Truncated bool          `json:"truncated,omitempty"`
}

// Preview preserves the original adapter API and refuses binary content. New
// callers that intentionally need bounded base64 should use PreviewWithOptions.
func (f *Fcheap) Preview(ctx context.Context, dir, stash, selector string, maxBytes int) (ArtifactPreview, error) {
	return f.PreviewWithOptions(ctx, dir, stash, selector, maxBytes, false)
}

// PreviewWithOptions restores a stash into an isolated temp directory and
// returns at most maxBytes from one safe relative path. With an explicit path,
// only that path and its ancestors are inspected. With no path, discovery is
// rejected as soon as either the walk-entry or regular-file cap is exceeded.
func (f *Fcheap) PreviewWithOptions(ctx context.Context, dir, stash, selector string, maxBytes int, allowBinary bool) (ArtifactPreview, error) {
	stashID, err := artifactStashID(stash)
	if err != nil {
		return ArtifactPreview{}, err
	}
	if err := ValidateArtifactPath(selector); err != nil {
		return ArtifactPreview{}, err
	}
	if !binExists(f.bin) {
		return ArtifactPreview{}, ErrToolMissing
	}
	if maxBytes <= 0 {
		maxBytes = 32 << 10
	}
	if maxBytes > 128<<10 {
		maxBytes = 128 << 10
	}
	tmp, err := os.MkdirTemp("", "cortex-artifact-*")
	if err != nil {
		return ArtifactPreview{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	_, stderr, code, err := f.execOnce(ctx, dir, "restore", stashID, "--to", tmp, "--json")
	if err != nil {
		return ArtifactPreview{}, err
	}
	if code != 0 {
		return ArtifactPreview{}, fmt.Errorf("fcheap restore exited %d: %s", code, clip(firstLine(stderr), 120))
	}

	preview := ArtifactPreview{StashID: stashID}
	selected := selector
	if selected != "" {
		fileInfo, infoErr := safeRestoredArtifactFile(tmp, selected)
		if infoErr != nil {
			return preview, infoErr
		}
		preview.Files = []PreviewFile{{Path: selected, Size: fileInfo.Size()}}
	} else {
		preview.Files, err = listRestoredArtifactFiles(tmp)
		if err != nil {
			return ArtifactPreview{}, err
		}
		if len(preview.Files) == 0 {
			return preview, nil
		}
		selected = preview.Files[0].Path
	}
	full, _, err := safeRestoredArtifactPath(tmp, selected)
	if err != nil {
		return preview, err
	}
	file, err := os.Open(full)
	if err != nil {
		return preview, err
	}
	defer func() { _ = file.Close() }()
	// Read one byte past the larger of the output limit and a small binary sniff
	// window. Memory remains bounded even when the restored file is enormous.
	readLimit := maxBytes + 1
	const binarySniffBytes = 8 << 10
	if readLimit < binarySniffBytes+1 {
		readLimit = binarySniffBytes + 1
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(readLimit)))
	if err != nil {
		return preview, err
	}
	binary := ArtifactContentIsBinary(data)
	if binary && !allowBinary {
		return preview, fmt.Errorf("artifact file %q is binary; retry with explicit binary permission", selected)
	}
	if len(data) > maxBytes {
		data = data[:maxBytes]
		preview.Truncated = true
	}
	preview.Selected = selected
	if !binary {
		preview.Encoding = "text"
		preview.Content = f.redact.String(string(data))
	} else {
		preview.Encoding = "base64"
		preview.Content = base64.StdEncoding.EncodeToString(data)
	}
	return preview, nil
}

func artifactStashID(stash string) (string, error) {
	if strings.TrimSpace(stash) != stash {
		return "", fmt.Errorf("artifact stash reference must not have surrounding whitespace")
	}
	id := strings.TrimPrefix(stash, "fcheap://stash/")
	if err := ValidateArtifactID(id); err != nil {
		return "", fmt.Errorf("invalid artifact stash id: %w", err)
	}
	return id, nil
}

// listRestoredArtifactFiles uses incremental directory reads rather than
// filepath.WalkDir, whose per-directory ReadDir call can allocate an unbounded
// entry slice before a callback gets a chance to enforce a cap.
func listRestoredArtifactFiles(root string) ([]PreviewFile, error) {
	dirs := []string{root}
	files := make([]PreviewFile, 0, MaxArtifactPreviewFiles)
	walked := 0
	for len(dirs) > 0 {
		dir := dirs[0]
		dirs = dirs[1:]
		handle, err := os.Open(dir)
		if err != nil {
			return nil, err
		}
		for {
			entries, readErr := handle.ReadDir(32)
			for _, entry := range entries {
				walked++
				if walked > MaxArtifactPreviewWalkEntries {
					_ = handle.Close()
					return nil, fmt.Errorf("artifact listing exceeds %d walked entries", MaxArtifactPreviewWalkEntries)
				}
				full := filepath.Join(dir, entry.Name())
				rel, relErr := filepath.Rel(root, full)
				if relErr != nil {
					_ = handle.Close()
					return nil, relErr
				}
				rel = filepath.ToSlash(rel)
				if err := ValidateArtifactPath(rel); err != nil {
					_ = handle.Close()
					return nil, fmt.Errorf("unsafe restored artifact path %q: %w", rel, err)
				}
				if entry.Type()&os.ModeSymlink != 0 {
					_ = handle.Close()
					return nil, fmt.Errorf("artifact stash contains symlink %q", rel)
				}
				if entry.IsDir() {
					dirs = append(dirs, full)
					continue
				}
				info, infoErr := entry.Info()
				if infoErr != nil {
					_ = handle.Close()
					return nil, infoErr
				}
				if !info.Mode().IsRegular() {
					_ = handle.Close()
					return nil, fmt.Errorf("artifact stash contains non-regular file %q", rel)
				}
				if len(files) >= MaxArtifactPreviewFiles {
					_ = handle.Close()
					return nil, fmt.Errorf("artifact listing exceeds %d regular files", MaxArtifactPreviewFiles)
				}
				files = append(files, PreviewFile{Path: rel, Size: info.Size()})
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = handle.Close()
				return nil, readErr
			}
		}
		if err := handle.Close(); err != nil {
			return nil, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func safeRestoredArtifactFile(root, relative string) (os.FileInfo, error) {
	_, info, err := safeRestoredArtifactPath(root, relative)
	return info, err
}

func safeRestoredArtifactPath(root, relative string) (string, os.FileInfo, error) {
	if err := ValidateArtifactPath(relative); err != nil {
		return "", nil, err
	}
	current := root
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil, fmt.Errorf("artifact file %q not found in stash", relative)
			}
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("artifact path traverses symlink %q", strings.Join(parts[:i+1], "/"))
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return "", nil, fmt.Errorf("artifact path component %q is not a directory", strings.Join(parts[:i+1], "/"))
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("artifact path %q is not a regular file", relative)
		}
		return current, info, nil
	}
	return "", nil, fmt.Errorf("artifact path is empty")
}
