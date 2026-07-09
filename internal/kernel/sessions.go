package kernel

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// SessionSummary is one session in the global, cross-workspace index — enough to
// audit at a glance (SPEC §8.1, §18.1) without opening the case file.
type SessionSummary struct {
	ID         string       `json:"id"`
	Goal       string       `json:"goal"`
	Phase      domain.Phase `json:"phase"`
	Mode       domain.Mode  `json:"mode"`
	Repository string       `json:"repository,omitempty"`
	Workspace  string       `json:"workspace,omitempty"`
	Slug       string       `json:"slug"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
	Verified   int          `json:"verified"` // proven verification receipts
	Required   int          `json:"required"` // verifiers the plan requires
	Active     bool         `json:"active"`   // phase is non-terminal (in-flight)
}

// SessionFilter narrows AllSessions. A zero filter returns everything.
type SessionFilter struct {
	Repo       string // match against slug or repository (substring); "" = all
	ActiveOnly bool   // only non-terminal (in-flight) sessions
}

// StaleSince reports whether an in-flight session hasn't advanced within age — a
// monitoring signal for forgotten or stuck work. Terminal sessions are never
// stale (they're done); a zero/negative age disables the check.
func (s SessionSummary) StaleSince(now time.Time, age time.Duration) bool {
	return s.Active && age > 0 && now.Sub(s.UpdatedAt) > age
}

// AllSessions enumerates every session under the central sessions root
// (config.SessionsRoot), newest-updated first. It is workspace-independent — it
// reads the global XDG state tree directly, so one call surfaces work across
// every repository. Sessions pinned to a repo-local store via cases_dir are not
// walked here; they stay visible through that workspace's `cortex list`.
func AllSessions(filter SessionFilter) ([]SessionSummary, error) {
	return allSessionsIn(config.SessionsRoot(), filter)
}

// ArchivedSessions lists sessions that have been moved to the archive (out of
// the active tree). Same shape and filters as AllSessions.
func ArchivedSessions(filter SessionFilter) ([]SessionSummary, error) {
	return allSessionsIn(config.ArchiveRoot(), filter)
}

func allSessionsIn(root string, filter SessionFilter) ([]SessionSummary, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no sessions opened yet
		}
		return nil, err
	}
	var out []SessionSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		store, err := casefs.New(filepath.Join(root, slug))
		if err != nil {
			continue
		}
		ids, err := store.List()
		if err != nil {
			continue
		}
		for _, id := range ids {
			c, err := store.Load(id)
			if err != nil {
				continue // stray dir without a case.json — skip, never fabricate
			}
			s := summarizeSession(c, slug, store)
			if filter.ActiveOnly && !s.Active {
				continue
			}
			if filter.Repo != "" && !strings.Contains(s.Slug, filter.Repo) && !strings.Contains(s.Repository, filter.Repo) {
				continue
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// SessionDetail is a fully-loaded session (case + ledgers) for a detail view.
type SessionDetail struct {
	Case     *domain.CaseFile
	Evidence []domain.Evidence
	Hyps     []domain.Hypothesis
	Receipts []domain.VerificationRecord
}

// LoadSession opens the store for a session's slug under the central sessions
// tree and loads its full record set. Workspace-independent, like AllSessions —
// the studio board uses it to show any session's detail regardless of repo.
func LoadSession(slug, taskID string) (SessionDetail, error) {
	store, err := casefs.New(filepath.Join(config.SessionsRoot(), slug))
	if err != nil {
		return SessionDetail{}, err
	}
	c, err := store.Load(taskID)
	if err != nil {
		return SessionDetail{}, err
	}
	ev, _ := store.Evidence(taskID)
	hyps, _ := store.Hypotheses(taskID)
	recs, _ := store.Verifications(taskID)
	return SessionDetail{Case: c, Evidence: ev, Hyps: hyps, Receipts: recs}, nil
}

func summarizeSession(c *domain.CaseFile, slug string, store *casefs.Store) SessionSummary {
	proven := 0
	if recs, err := store.Verifications(c.ID); err == nil {
		for _, r := range recs {
			if r.Proven() {
				proven++
			}
		}
	}
	return SessionSummary{
		ID: c.ID, Goal: c.Goal, Phase: c.Status, Mode: c.Mode,
		Repository: c.Workspace.Repository, Workspace: c.Workspace.Root,
		Slug:      slug,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		Verified: proven, Required: len(c.VerificationRequired),
		Active: !c.Status.IsTerminal(),
	}
}
