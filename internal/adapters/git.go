package adapters

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Git adapts the git CLI for workspace identity and change (diff) evidence
// (SPEC §11.3 git.Adapter). It is the one adapter Cortex relies on for
// orientation and scope-drift detection, so it stays fully concrete.
type Git struct{ tool }

// NewGit builds a git adapter.
func NewGit() *Git { return &Git{tool: newTool("git", 20*time.Second)} }

func (g *Git) Name() string { return "git" }

func (g *Git) Capabilities() []Capability { return []Capability{CapabilityStructure} }

// Health reports whether git is available. It does not require a repo — that is
// checked per-operation so a non-repo workspace degrades rather than fails.
func (g *Git) Health(ctx context.Context) error { return g.healthByVersion(ctx) }

// Execute routes git operations: "status" (workspace identity) and
// "changed_files" (diff for scope-drift comparison).
func (g *Git) Execute(ctx context.Context, req Request) (Result, error) {
	dir := req.Str("dir")
	switch req.Operation {
	case "status", "":
		return g.status(ctx, dir)
	case "changed_files":
		return g.changed(ctx, dir, req.Str("since"), boolOf(req.Input["staged"]))
	default:
		return Result{Tool: "git", Operation: req.Operation, Status: StatusError,
			Summary: "unknown git operation: " + req.Operation}, nil
	}
}

// WorkspaceInfo is the parsed identity of a git working tree.
type WorkspaceInfo struct {
	IsRepo     bool
	Repository string
	Branch     string
	Commit     string
	Dirty      bool
}

// Status returns the workspace identity directly (used by the orient phase,
// which needs typed fields rather than facts).
func (g *Git) Status(ctx context.Context, dir string) (WorkspaceInfo, error) {
	info := WorkspaceInfo{Repository: filepath.Base(dir)}
	// A missing git binary is a hard error, distinct from "not a repo": the
	// orient phase and scope-drift both depend on git, so silently reporting
	// "not a repository" when git is absent would hide a real environment gap.
	if !binExists(g.bin) {
		return info, ErrToolMissing
	}
	inside, _, _, err := g.exec(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return info, nil // not a repo — degrade, don't error
	}
	info.IsRepo = true
	if b, _, _, err := g.exec(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(b)
	}
	if c, _, _, err := g.exec(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
		info.Commit = strings.TrimSpace(c)
	}
	if top, _, _, err := g.exec(ctx, dir, "rev-parse", "--show-toplevel"); err == nil {
		if t := strings.TrimSpace(top); t != "" {
			info.Repository = filepath.Base(t)
		}
	}
	if st, _, _, err := g.exec(ctx, dir, "status", "--porcelain"); err == nil {
		info.Dirty = strings.TrimSpace(st) != ""
	}
	return info, nil
}

func (g *Git) status(ctx context.Context, dir string) (Result, error) {
	info, err := g.Status(ctx, dir)
	if err != nil {
		return unavailable("git", "status", err.Error()), nil
	}
	if !info.IsRepo {
		return Result{Tool: "git", Operation: "status", Status: StatusPartial,
			Summary: "workspace is not a git repository"}, nil
	}
	claim := "workspace is git repo " + info.Repository + " on branch " + info.Branch + " at " + info.Commit
	if info.Dirty {
		claim += " (working tree dirty)"
	}
	return Result{
		Tool: "git", Operation: "status", Status: StatusAuthoritative,
		Summary: claim,
		Facts:   []Fact{{Kind: "code_location", Claim: claim, Confidence: "high"}},
	}, nil
}

// ChangedFiles returns the list of files changed in the working tree (or staged,
// or since a ref). Used by scope-drift detection (SPEC §13.2).
func (g *Git) ChangedFiles(ctx context.Context, dir, since string, staged bool) ([]string, error) {
	args := []string{"diff", "--name-only"}
	switch {
	case staged:
		args = append(args, "--cached")
	case since != "":
		args = append(args, since+"...HEAD")
	}
	out, serr, code, err := g.exec(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	// A non-zero exit (e.g. a bad/unfetched base ref) is a real error, NOT an
	// empty diff — otherwise a review of a bogus base would silently see 0 files
	// and report a false all-clear.
	if code != 0 {
		return nil, fmt.Errorf("git diff failed: %s", firstNonEmpty(firstLine(serr), fmt.Sprintf("exit %d", code)))
	}
	files := splitNonEmpty(out)
	// Untracked files are part of the working tree, not a committed range. Include
	// them only in working-tree mode, so a base…HEAD or staged diff reflects the
	// actual commits and isn't polluted by local scratch files.
	if since == "" && !staged {
		unt, _, _, _ := g.exec(ctx, dir, "ls-files", "--others", "--exclude-standard")
		files = append(files, splitNonEmpty(unt)...)
	}
	return dedupeSorted(files), nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (g *Git) changed(ctx context.Context, dir, since string, staged bool) (Result, error) {
	files, err := g.ChangedFiles(ctx, dir, since, staged)
	if err != nil {
		return unavailable("git", "changed_files", err.Error()), nil
	}
	facts := make([]Fact, 0, len(files))
	for _, f := range files {
		facts = append(facts, Fact{Kind: "code_location", Claim: "changed file " + f, Confidence: "high",
			Location: &Location{File: f}})
	}
	return Result{
		Tool: "git", Operation: "changed_files", Status: StatusAuthoritative,
		Summary: pluralize(len(files), "changed file"),
		Facts:   facts,
	}, nil
}

// line runs a git command and returns its first line trimmed (read-only).
func (g *Git) line(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, _, err := g.exec(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(firstLine(out)), nil
}

// RemoteURL returns the URL of a git remote (default "origin").
func (g *Git) RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	return g.line(ctx, dir, "remote", "get-url", remote)
}

// CurrentBranch returns the checked-out branch name (or "HEAD" when detached).
func (g *Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	return g.line(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch returns the remote's default branch (e.g. main), falling back to
// main/master when the remote HEAD isn't recorded locally.
func (g *Git) DefaultBranch(ctx context.Context, dir, remote string) string {
	if remote == "" {
		remote = "origin"
	}
	if ref, err := g.line(ctx, dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD"); err == nil && ref != "" {
		if i := strings.LastIndex(ref, "/"); i >= 0 {
			return ref[i+1:]
		}
	}
	for _, b := range []string{"main", "master"} {
		// `rev-parse --verify --quiet <b>` exits non-zero and prints NOTHING when
		// the branch is absent; the runner reports that as (empty, nil), so key off
		// the output (a resolved sha), not err — else the first branch always wins.
		if sha, err := g.line(ctx, dir, "rev-parse", "--verify", "--quiet", b); err == nil && sha != "" {
			return b
		}
	}
	return "main"
}

// MergeBase returns the common ancestor commit of two refs (the fork point).
func (g *Git) MergeBase(ctx context.Context, dir, a, b string) (string, error) {
	return g.line(ctx, dir, "merge-base", a, b)
}

// FetchRef fetches a refspec from a remote into a local ref (a write). It tries
// each candidate refspec in order and returns nil on the first success — hosts
// expose PRs under different refs (GitHub pull/N/head, Bitbucket pull-requests/
// N/from). Returns the last error if none succeed.
func (g *Git) FetchRef(ctx context.Context, dir, remote string, refspecs ...string) error {
	if remote == "" {
		remote = "origin"
	}
	var lastErr error
	for _, rs := range refspecs {
		// Force-update the local ref (+) so a re-review of a force-pushed / rebased
		// PR fetches the new head instead of failing on a non-fast-forward.
		if !strings.HasPrefix(rs, "+") {
			rs = "+" + rs
		}
		_, _, code, err := g.execOnce(ctx, dir, "fetch", "--quiet", remote, rs)
		if err == nil && code == 0 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("git fetch %s %s: exit %d", remote, rs, code)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no refspec provided")
	}
	return lastErr
}

// Checkout switches the working tree to a ref (a write).
func (g *Git) Checkout(ctx context.Context, dir, ref string) error {
	_, _, code, err := g.execOnce(ctx, dir, "checkout", "--quiet", ref)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git checkout %s: exit %d", ref, code)
	}
	return nil
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func dedupeSorted(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
