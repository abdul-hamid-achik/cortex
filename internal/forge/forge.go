// Package forge maps a git hosting provider (GitHub, Bitbucket, …) to the git
// refspecs under which it exposes pull/merge requests. It is deliberately pure
// logic — no git or network execution — so the kernel can resolve a PR to a
// fetchable ref host-agnostically and degrade honestly when a host can't be
// fetched by ref alone.
package forge

import (
	"fmt"
	"strings"
)

// Forge identifies a hosting provider and how it names pull-request refs.
type Forge struct {
	Name string // "github" | "bitbucket" | "git"
	// prTerm is the provider's word for a PR, used in user-facing guidance.
	prTerm string
	// refPatterns are the remote refs to try for a PR head, most-likely first
	// (%d is the PR number). Different hosts expose PRs differently, and some
	// (e.g. Bitbucket Cloud) expose no PR head ref at all — hence a list plus a
	// documented fallback.
	refPatterns []string
}

var (
	github = Forge{Name: "github", prTerm: "pull request",
		refPatterns: []string{"refs/pull/%d/head"}}
	// Bitbucket Server exposes refs/pull-requests/<id>/from; Bitbucket Cloud does
	// not expose a PR head ref, so a ref-fetch may fail there (handled by the
	// caller's fallback). We also try the GitHub-style ref for GitHub-compatible
	// mirrors.
	bitbucket = Forge{Name: "bitbucket", prTerm: "pull request",
		refPatterns: []string{"refs/pull-requests/%d/from", "refs/pull/%d/head"}}
	// generic covers self-hosted GitLab (merge-requests) and anything unknown:
	// try the common ref shapes.
	generic = Forge{Name: "git", prTerm: "pull/merge request",
		refPatterns: []string{"refs/pull/%d/head", "refs/pull-requests/%d/from", "refs/merge-requests/%d/head"}}
)

// Detect returns the forge for a git remote URL (SSH or HTTPS). Unknown hosts
// get the generic forge, which tries the common ref shapes.
func Detect(remoteURL string) Forge {
	h := strings.ToLower(remoteURL)
	switch {
	case strings.Contains(h, "github.com") || strings.Contains(h, "github."):
		return github
	case strings.Contains(h, "bitbucket.org") || strings.Contains(h, "bitbucket"):
		return bitbucket
	case strings.Contains(h, "gitlab"):
		return generic
	default:
		return generic
	}
}

// PRHeadRefspecs returns the candidate `<remote-ref>:<localRef>` refspecs to try
// fetching PR number n's head into localRef, most-likely first.
func (f Forge) PRHeadRefspecs(n int, localRef string) []string {
	out := make([]string, 0, len(f.refPatterns))
	for _, p := range f.refPatterns {
		out = append(out, fmt.Sprintf(p, n)+":"+localRef)
	}
	return out
}

// PRTerm is the provider's human word for a pull/merge request.
func (f Forge) PRTerm() string { return f.prTerm }

// FetchHint tells the user how to review a PR this forge couldn't fetch by ref
// alone (notably Bitbucket Cloud), keeping cortex honest instead of guessing.
func (f Forge) FetchHint(n int) string {
	return fmt.Sprintf("couldn't fetch %s #%d by git ref for a %s remote — check out its branch, then run `cortex review --base <target-branch>`",
		f.prTerm, n, f.Name)
}
