package kernel

import (
	"sort"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// Overview is a cross-repository rollup of every Cortex session — the "how am I
// using cortex overall" dashboard that per-workspace metrics never gave. It is
// computed from session summaries (cheap: no per-case ledger reads), so elapsed
// time is the created→last-updated span.
type Overview struct {
	Sessions             int        `json:"sessions"`
	Active               int        `json:"active"`
	Stale                int        `json:"stale"`
	Completed            int        `json:"completed"`
	Verified             int        `json:"verified"`
	CompletionRate       float64    `json:"completionRate"`
	VerifiedRate         float64    `json:"verifiedRate"`
	MeanTimeToCompleteMs int64      `json:"meanTimeToCompleteMs,omitempty"`
	Repos                []RepoStat `json:"repos,omitempty"`
}

// RepoStat is one repository's slice of the overview.
type RepoStat struct {
	Repo      string `json:"repo"`
	Sessions  int    `json:"sessions"`
	Active    int    `json:"active"`
	Completed int    `json:"completed"`
}

// BuildOverview aggregates every session under the central tree. staleAfter/now
// drive the stale count (0 disables it). Workspace-independent.
func BuildOverview(staleAfter time.Duration, now time.Time) (Overview, error) {
	sessions, err := AllSessions(SessionFilter{})
	if err != nil {
		return Overview{}, err
	}
	var o Overview
	byRepo := map[string]*RepoStat{}
	repoOf := func(slug string) *RepoStat {
		if rs := byRepo[slug]; rs != nil {
			return rs
		}
		rs := &RepoStat{Repo: slug}
		byRepo[slug] = rs
		return rs
	}
	var elapsedSum int64
	for _, s := range sessions {
		o.Sessions++
		rs := repoOf(s.Slug)
		rs.Sessions++
		if s.Active {
			o.Active++
			rs.Active++
		}
		if s.StaleSince(now, staleAfter) {
			o.Stale++
		}
		if s.Phase == domain.PhaseComplete {
			o.Completed++
			rs.Completed++
			elapsedSum += s.UpdatedAt.Sub(s.CreatedAt).Milliseconds()
			if s.VerificationOutcome == VerificationVerified {
				o.Verified++
			}
		}
	}
	if o.Sessions > 0 {
		o.CompletionRate = ratio(o.Completed, o.Sessions)
		o.VerifiedRate = ratio(o.Verified, o.Sessions)
	}
	if o.Completed > 0 {
		o.MeanTimeToCompleteMs = elapsedSum / int64(o.Completed)
	}
	o.Repos = make([]RepoStat, 0, len(byRepo))
	for _, rs := range byRepo {
		o.Repos = append(o.Repos, *rs)
	}
	sort.Slice(o.Repos, func(i, j int) bool {
		if o.Repos[i].Sessions != o.Repos[j].Sessions {
			return o.Repos[i].Sessions > o.Repos[j].Sessions
		}
		return o.Repos[i].Repo < o.Repos[j].Repo
	})
	return o, nil
}
