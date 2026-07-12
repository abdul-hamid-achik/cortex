package kernel

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// ScopeReport is the result of comparing observed changes to the declared
// boundary (SPEC §13.2). Drift does not auto-fail a task; it makes accidental
// expansion visible.
type ScopeReport struct {
	Scope           string   `json:"scope"` // within_boundary | drift_detected | no_boundary
	UnexpectedFiles []string `json:"unexpectedFiles,omitempty"`
	Risk            string   `json:"risk"`
	Action          string   `json:"action,omitempty"`
	ChangedFiles    []string `json:"changedFiles,omitempty"`
}

// detectScopeDrift compares the working-tree diff against the case's boundary.
// A file counts as in-boundary only when it matches the canonical repo-relative
// path or an explicit one-level/recursive glob. Plan normalizes absolute paths
// inside the workspace, so suffix matching would only hide same-name drift.
func (k *Kernel) detectScopeDrift(ctx context.Context, c *domain.CaseFile, changed []string) ScopeReport {
	if !c.ChangeBoundary.Declared() {
		return ScopeReport{Scope: "no_boundary", Risk: "unknown", ChangedFiles: changed,
			Action: "no change boundary was declared; declare one at plan time to detect drift"}
	}
	var unexpected []string
	for _, f := range changed {
		if !k.inBoundary(f, c.ChangeBoundary.Files) {
			unexpected = append(unexpected, f)
		}
	}
	if len(unexpected) == 0 {
		return ScopeReport{Scope: "within_boundary", Risk: "low", ChangedFiles: changed}
	}
	risk := "medium"
	if len(unexpected) > 3 {
		risk = "high"
	}
	return ScopeReport{
		Scope:           "drift_detected",
		UnexpectedFiles: unexpected,
		Risk:            risk,
		Action:          "expand the plan to cover these files, or revert the unrelated changes",
		ChangedFiles:    changed,
	}
}

// inBoundary reports whether a changed path is covered by any boundary entry.
func (k *Kernel) inBoundary(changed string, boundary []string) bool {
	changed = normalizePath(changed)
	for _, b := range boundary {
		b = normalizePath(b)
		switch {
		case b == changed:
			return true
		case strings.HasSuffix(b, "/**") && strings.HasPrefix(changed, strings.TrimSuffix(b, "**")):
			return true
		case strings.HasSuffix(b, "/*") && filepath.Dir(changed) == filepath.Dir(strings.TrimSuffix(b, "*")+"x"):
			return true
		}
	}
	return false
}

func normalizePath(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
}

// mergeChangedFiles makes the repository-derived diff authoritative while
// retaining caller-provided hints as additive context. A caller can point out an
// extra generated/external change, but can never omit a Git-observed file to hide
// scope drift.
func mergeChangedFiles(gitFiles, hints []string) []string {
	out := make([]string, 0, len(gitFiles)+len(hints))
	seen := make(map[string]bool, len(gitFiles)+len(hints))
	for _, group := range [][]string{gitFiles, hints} {
		for _, raw := range group {
			file := normalizePath(raw)
			if file == "" || seen[file] {
				continue
			}
			seen[file] = true
			out = append(out, file)
		}
	}
	return out
}
