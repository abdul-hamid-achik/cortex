package kernel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// headCacheTTL bounds how often a burst of evidence stamps re-asks git for
// HEAD. One investigation round stamps a dozen facts; one rev-parse suffices.
const headCacheTTL = 2 * time.Second

// maxFreshnessCommits bounds how many distinct recording commits one freshness
// pass diffs against HEAD, so a long case cannot turn status into a git storm.
const maxFreshnessCommits = 16

// headCache is shared by pointer so kernels copied by value (recall indexing)
// share one cache instead of copying a mutex.
type headCache struct {
	mu   sync.Mutex
	head string
	at   time.Time
}

// headCommit returns the workspace HEAD, briefly cached. Empty when git is
// unavailable — a record without a commit is simply exempt from freshness.
func (k *Kernel) headCommit(ctx context.Context) string {
	if k.git == nil {
		return ""
	}
	cache := k.headCache
	if cache == nil {
		cache = &headCache{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := k.now()
	if cache.head != "" && now.Sub(cache.at) < headCacheTTL && now.Sub(cache.at) >= 0 {
		return cache.head
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	head, err := k.git.Head(ctx, k.cfg.Workspace)
	if err != nil {
		return ""
	}
	cache.head, cache.at = head, now
	return head
}

// StaleEvidence flags one evidence record whose located file changed after
// the record was written. The claim may still hold; it is no longer proven by
// the file as it was read.
type StaleEvidence struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Commit string `json:"commit,omitempty"`
	Reason string `json:"reason"`
}

// freshnessProbe answers "did any of these files change since commit X?" with
// one git call per distinct commit, cached for the life of the probe.
type freshnessProbe struct {
	k        *Kernel
	ctx      context.Context
	head     string
	changed  map[string]map[string]bool
	failed   map[string]string
	warnings []string
}

func (k *Kernel) newFreshnessProbe(ctx context.Context) *freshnessProbe {
	return &freshnessProbe{k: k, ctx: ctx, head: k.headCommit(ctx), changed: map[string]map[string]bool{}, failed: map[string]string{}}
}

// available reports whether freshness can be evaluated at all.
func (p *freshnessProbe) available() bool { return p.k.git != nil && p.head != "" }

func (p *freshnessProbe) changedSince(commit string) (map[string]bool, bool) {
	if set, ok := p.changed[commit]; ok {
		return set, true
	}
	if _, failed := p.failed[commit]; failed {
		return nil, false
	}
	if len(p.changed)+len(p.failed) >= maxFreshnessCommits {
		p.failed[commit] = "bounded"
		p.warnings = appendOnce(p.warnings, fmt.Sprintf("freshness check bounded to %d distinct recording commits; older records were not checked", maxFreshnessCommits))
		return nil, false
	}
	files, err := p.k.git.ChangedSince(p.ctx, p.k.cfg.Workspace, commit)
	if err != nil {
		p.failed[commit] = err.Error()
		p.warnings = appendOnce(p.warnings, fmt.Sprintf("could not check freshness of records written at %s: %s", clipStr(commit, 12), err))
		return nil, false
	}
	set := make(map[string]bool, len(files))
	for _, f := range files {
		set[p.k.workspaceRelative(f)] = true
	}
	p.changed[commit] = set
	return set, true
}

// stale reports whether any of files changed since commit, with a reason.
func (p *freshnessProbe) stale(commit string, files []string) (bool, string) {
	if !p.available() || strings.TrimSpace(commit) == "" || len(files) == 0 {
		return false, ""
	}
	set, ok := p.changedSince(commit)
	if !ok {
		return false, ""
	}
	for _, f := range files {
		rel := p.k.workspaceRelative(f)
		if rel == "" || rel == "." {
			continue
		}
		if set[rel] {
			if commit == p.head {
				return true, fmt.Sprintf("%s changed in the working tree since it was recorded", rel)
			}
			return true, fmt.Sprintf("%s changed between %s and HEAD", rel, clipStr(commit, 12))
		}
		// A module-level path (directory) is stale when any changed file lives under it.
		for changed := range set {
			if strings.HasPrefix(changed, rel+"/") {
				return true, fmt.Sprintf("%s changed under %s since %s", changed, rel, clipStr(commit, 12))
			}
		}
	}
	return false, ""
}

// staleEvidence evaluates freshness for located records. Records without a
// commit or a file location are exempt (legacy, human, or graph-only claims),
// and so are files in except — a change case's own declared edits.
func (k *Kernel) staleEvidence(ctx context.Context, evidence []domain.Evidence, except map[string]bool) ([]StaleEvidence, []string) {
	probe := k.newFreshnessProbe(ctx)
	if !probe.available() {
		return nil, nil
	}
	var out []StaleEvidence
	for _, ev := range evidence {
		if ev.Commit == "" || ev.Location == nil || strings.TrimSpace(ev.Location.File) == "" {
			continue
		}
		if except[k.workspaceRelative(ev.Location.File)] {
			continue
		}
		if stale, reason := probe.stale(ev.Commit, []string{ev.Location.File}); stale {
			out = append(out, StaleEvidence{ID: ev.ID, File: k.workspaceRelative(ev.Location.File), Commit: ev.Commit, Reason: reason})
		}
	}
	return out, probe.warnings
}

// expectedEdits returns the declared change boundary as a set once a change
// case is editing: the agent's own edits to those files are the change, not
// stale reads, so freshness only flags files outside the boundary.
func expectedEdits(k *Kernel, c *domain.CaseFile) map[string]bool {
	if c == nil || c.Mode != domain.ModeChange {
		return nil
	}
	switch c.Status {
	case domain.PhaseChanging, domain.PhaseVerifying, domain.PhasePersisting, domain.PhaseComplete:
	default:
		return nil
	}
	if len(c.ChangeBoundary.Files) == 0 {
		return nil
	}
	out := make(map[string]bool, len(c.ChangeBoundary.Files))
	for _, f := range c.ChangeBoundary.Files {
		out[k.workspaceRelative(f)] = true
	}
	return out
}

// workspaceRelative normalizes an absolute or relative file path to the
// forward-slash, workspace-relative form git reports.
func (k *Kernel) workspaceRelative(file string) string {
	f := strings.ReplaceAll(strings.TrimSpace(file), "\\", "/")
	ws := strings.ReplaceAll(strings.TrimRight(k.cfg.Workspace, "/"), "\\", "/")
	if ws != "" && strings.HasPrefix(f, ws+"/") {
		f = strings.TrimPrefix(f, ws+"/")
	}
	return domain.NormalizeModulePath(f)
}

func appendOnce(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

// staleSupportWarning reports when an unresolved hypothesis rests on evidence
// whose file changed after it was recorded. Verification still runs — the
// warning says the reasoning behind the claim may be out of date.
func (k *Kernel) staleSupportWarning(ctx context.Context, c *domain.CaseFile) string {
	hyps, err := k.store.Hypotheses(c.ID)
	if err != nil || len(hyps) == 0 {
		return ""
	}
	supports := map[string]bool{}
	for _, h := range hyps {
		if h.Status == domain.HypRejected {
			continue
		}
		for _, id := range h.Supports {
			supports[id] = true
		}
	}
	if len(supports) == 0 {
		return ""
	}
	evidence, err := k.store.Evidence(c.ID)
	if err != nil {
		return ""
	}
	var supporting []domain.Evidence
	for _, ev := range evidence {
		if supports[ev.ID] {
			supporting = append(supporting, ev)
		}
	}
	stale, _ := k.staleEvidence(ctx, supporting, expectedEdits(k, c))
	if len(stale) == 0 {
		return ""
	}
	ids := make([]string, 0, len(stale))
	for _, s := range stale {
		ids = append(ids, s.ID)
	}
	return fmt.Sprintf("%d supporting evidence record(s) describe files that changed after they were recorded (%s) — the hypothesis may rest on stale reads; re-investigate before trusting the claim", len(stale), strings.Join(clipList(ids, 5), ", "))
}
