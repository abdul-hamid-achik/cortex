package kernel

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// PruneFailure records one session that could not be pruned and why.
type PruneFailure struct {
	TaskID string `json:"taskId"`
	Error  string `json:"error"`
}

// PruneStaleReport summarizes a prune operation. Stale lists every in-flight
// session older than OlderThan; Pruned/Skipped/Failed are populated only when
// Applied is true.
type PruneStaleReport struct {
	OlderThan time.Duration    `json:"olderThan"`
	Applied   bool             `json:"applied"`
	Stale     []SessionSummary `json:"stale"`
	Pruned    []string         `json:"pruned,omitempty"`
	Skipped   []string         `json:"skipped,omitempty"`
	Failed    []PruneFailure   `json:"failed,omitempty"`
}

// errSessionNotInFlight marks a session that turned terminal between listing and
// pruning, so it is skipped (nothing left to shelve) rather than counted failed.
var errSessionNotInFlight = errors.New("session is no longer in flight")

// PruneStale finds in-flight sessions that have not advanced within age. With
// apply=false it only reports them (a dry run that changes nothing). With
// apply=true each is aborted (recording the reason) and archived out of the
// active tree, so sessions/overview/studio stay focused on live work. Pruning is
// recoverable — UnarchiveSession restores an archived session — and honest,
// because the abort records why the session was retired rather than silently
// hiding active work. repo optionally narrows the operation to one repository.
func PruneStale(now time.Time, age time.Duration, apply bool, repo string) (PruneStaleReport, error) {
	sessions, err := AllSessions(SessionFilter{ActiveOnly: true, Repo: repo})
	if err != nil {
		return PruneStaleReport{}, err
	}
	rep := PruneStaleReport{OlderThan: age, Applied: apply, Stale: []SessionSummary{}}
	for _, s := range sessions {
		if !s.StaleSince(now, age) {
			continue
		}
		rep.Stale = append(rep.Stale, s)
		if !apply {
			continue
		}
		switch err := shelveStaleSession(s.ID, now, age); {
		case err == nil:
			rep.Pruned = append(rep.Pruned, s.ID)
		case errors.Is(err, errSessionNotInFlight):
			rep.Skipped = append(rep.Skipped, s.ID)
		default:
			rep.Failed = append(rep.Failed, PruneFailure{TaskID: s.ID, Error: err.Error()})
		}
	}
	return rep, nil
}

// shelveStaleSession retires one forgotten in-flight session: release any active
// change lease, mark the case abandoned with a prune reason, record the phase
// transition, and move the case to the archive. It locates the session's store
// directly so it works across repositories without a workspace-bound kernel.
func shelveStaleSession(taskID string, now time.Time, age time.Duration) error {
	slug, store, err := LocateSession(taskID)
	if err != nil {
		return err
	}
	c, err := store.Load(taskID)
	if err != nil {
		return err
	}
	if c.Status.IsTerminal() {
		return errSessionNotInFlight
	}
	from := c.Status
	if c.ChangeLease != nil && c.ChangeLease.Active(now) {
		if err := c.ChangeLease.Release(c.ChangeLease.Actor, now); err != nil {
			return fmt.Errorf("cannot release change lease: %w", err)
		}
	}
	c.Status = domain.PhaseAbandoned
	c.BlockedReason = "pruned: no activity for " + FormatAge(age)
	if err := store.Save(c); err != nil {
		return err
	}
	_ = store.AppendPhaseEvent(taskID, casefs.PhaseEvent{Timestamp: now.UTC(), From: from, To: domain.PhaseAbandoned})
	return store.MoveTaskTo(taskID, filepath.Join(config.ArchiveRoot(), slug))
}

// FormatAge renders an age/threshold compactly: whole days as "7d", whole hours
// as "24h", otherwise Go's duration string.
func FormatAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	default:
		return d.String()
	}
}
