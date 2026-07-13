package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// MaxSessionQueryBytes bounds non-interactive CLI/MCP input before Cortex
// walks the central session tree. Studio applies a smaller interactive cap.
const MaxSessionQueryBytes = 4 << 10

// SessionSummary is one session in the global, cross-workspace index — enough to
// audit at a glance without opening the case file.
type SessionSummary struct {
	ID                  string              `json:"id"`
	Goal                string              `json:"goal"`
	Phase               domain.Phase        `json:"phase"`
	Mode                domain.Mode         `json:"mode"`
	Repository          string              `json:"repository,omitempty"`
	Workspace           string              `json:"workspace,omitempty"`
	Slug                string              `json:"slug"`
	CreatedAt           time.Time           `json:"createdAt"`
	UpdatedAt           time.Time           `json:"updatedAt"`
	Verified            int                 `json:"verified"` // required verifiers satisfied without a named-claim failure
	Required            int                 `json:"required"` // verifiers the plan requires
	VerificationOutcome VerificationOutcome `json:"verificationOutcome"`
	Active              bool                `json:"active"` // phase is non-terminal (in-flight)
}

// SessionFilter narrows AllSessions. A zero filter returns everything.
type SessionFilter struct {
	Repo       string // match against slug or repository (substring); "" = all
	ActiveOnly bool   // only non-terminal (in-flight) sessions
	Query      string // case-insensitive AND-token match across session identity and status fields
}

type revisionLookup struct {
	revision adapters.Revision
	err      error
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
	if !utf8.ValidString(filter.Query) || len(filter.Query) > MaxSessionQueryBytes {
		return nil, fmt.Errorf("session query must be UTF-8 and at most %d bytes", MaxSessionQueryBytes)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no sessions opened yet
		}
		return nil, err
	}
	var out []SessionSummary
	revisions := make(map[string]revisionLookup)
	git := adapters.NewGit()
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
			s := summarizeSession(c, slug, store, git, revisions)
			if filter.ActiveOnly && !s.Active {
				continue
			}
			if filter.Repo != "" && !strings.Contains(s.Slug, filter.Repo) && !strings.Contains(s.Repository, filter.Repo) {
				continue
			}
			if !sessionMatchesQuery(s, filter.Query) {
				continue
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// sessionMatchesQuery provides one search contract for the CLI, MCP, and
// Studio surfaces. Whitespace-separated tokens are ANDed so a query such as
// "billing planned" can combine repository and phase without introducing a
// surface-specific query language.
func sessionMatchesQuery(s SessionSummary, query string) bool {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		s.ID,
		s.Goal,
		string(s.Phase),
		string(s.Mode),
		s.Repository,
		s.Workspace,
		s.Slug,
		string(s.VerificationOutcome),
	}, "\x00"))
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
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
	ev, err := store.Evidence(taskID)
	if err != nil {
		return SessionDetail{}, fmt.Errorf("load evidence: %w", err)
	}
	hyps, err := store.Hypotheses(taskID)
	if err != nil {
		return SessionDetail{}, fmt.Errorf("load hypotheses: %w", err)
	}
	recs, err := store.Verifications(taskID)
	if err != nil {
		return SessionDetail{}, fmt.Errorf("load verifications: %w", err)
	}
	return SessionDetail{Case: c, Evidence: ev, Hyps: hyps, Receipts: recs}, nil
}

func summarizeSession(c *domain.CaseFile, slug string, store *casefs.Store, git *adapters.Git, revisions map[string]revisionLookup) SessionSummary {
	receipts, _ := store.Verifications(c.ID)
	if !c.Status.IsTerminal() {
		lookup, ok := revisions[c.Workspace.Root]
		if !ok {
			lookup.revision, lookup.err = git.CurrentRevision(context.Background(), c.Workspace.Root)
			revisions[c.Workspace.Root] = lookup
		}
		receipts, _ = verificationReceiptsAtRevision(receipts, lookup.revision, lookup.err)
	}
	assessment := assessCaseVerification(c, receipts)
	verifiedRequired := len(assessment.SatisfiedRequired)
	// The compact N/M count must not look green when a current named claim is
	// non-passing or failed, even if all verifier labels happened to run.
	if len(assessment.NonPassingClaims) > 0 || len(assessment.FailedClaims) > 0 {
		verifiedRequired = 0
	}
	r := redact.New(config.For(c.Workspace.Root).RedactLiterals...)
	return SessionSummary{
		ID: c.ID, Goal: r.String(c.Goal), Phase: c.Status, Mode: c.Mode,
		Repository: r.String(c.Workspace.Repository), Workspace: r.String(c.Workspace.Root),
		Slug:      r.String(slug),
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		Verified: verifiedRequired, Required: len(c.VerificationRequired),
		VerificationOutcome: assessment.Outcome,
		Active:              !c.Status.IsTerminal(),
	}
}
