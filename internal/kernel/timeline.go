package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// TimelineEntry is one dated event in a session's activity feed.
type TimelineEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"` // phase | evidence | command | verification
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
	Ref       string    `json:"ref,omitempty"` // evidence id, for read-evidence follow-up
}

// LocateSession finds which slug store under the central sessions tree holds
// taskID and returns it opened. Workspace-independent, so `cortex timeline <id>`
// works from anywhere regardless of which repo the session belongs to.
func LocateSession(taskID string) (string, *casefs.Store, error) {
	if slug, store, err := locateUnder(config.SessionsRoot(), taskID); err == nil {
		return slug, store, nil
	}
	// Fallback: a session kept repo-local (opt-in cases_dir, or a pre-existing
	// .cortex/cases that DefaultCasesDir honors) in the CURRENT workspace — the
	// central-tree walk can't see those. Only probe an EXISTING dir so a lookup
	// never creates one.
	cfg := config.For("")
	if fi, statErr := os.Stat(cfg.CasesDir); statErr == nil && fi.IsDir() {
		if store, err := casefs.New(cfg.CasesDir); err == nil {
			if _, err := store.Load(taskID); err == nil {
				return config.Slug(cfg.Workspace), store, nil
			}
		}
	}
	return "", nil, fmt.Errorf("session %s: %w", taskID, casefs.ErrNotFound)
}

// locateUnder finds the slug store beneath root that holds taskID (used for both
// the active sessions tree and the archive).
func locateUnder(root, taskID string) (string, *casefs.Store, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("session %s: %w", taskID, casefs.ErrNotFound)
		}
		return "", nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		store, err := casefs.New(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		if _, err := store.Load(taskID); err == nil {
			return e.Name(), store, nil
		}
	}
	return "", nil, fmt.Errorf("session %s: %w", taskID, casefs.ErrNotFound)
}

// Timeline merges a session's phase history, evidence, audited commands, and
// verification receipts into one time-sorted feed — this is what finally
// surfaces commands.jsonl, the audit log that until now had no reader outside
// the metrics path (SPEC §18.1).
func Timeline(taskID string) ([]TimelineEntry, error) {
	_, store, err := LocateSession(taskID)
	if err != nil {
		return nil, err
	}
	return timelineFromStore(store, taskID), nil
}

// timelineFromStore builds the merged, time-sorted feed for an already-located
// store — so callers that already hold the store (e.g. ShowSession) don't walk
// the tree twice.
func timelineFromStore(store *casefs.Store, taskID string) []TimelineEntry {
	var out []TimelineEntry
	if evs, err := store.PhaseEvents(taskID); err == nil {
		for _, e := range evs {
			out = append(out, TimelineEntry{Timestamp: e.Timestamp, Kind: "phase", Summary: string(e.From) + " → " + string(e.To)})
		}
	}
	if evs, err := store.Evidence(taskID); err == nil {
		for _, e := range evs {
			out = append(out, TimelineEntry{Timestamp: e.Timestamp, Kind: "evidence", Summary: e.Claim, Detail: string(e.Kind), Ref: e.ID})
		}
	}
	if cmds, err := store.Commands(taskID); err == nil {
		for _, c := range cmds {
			out = append(out, TimelineEntry{Timestamp: c.Timestamp, Kind: "command", Summary: c.Tool + "." + c.Operation + " (" + c.Status + ")", Detail: c.ActionClass})
		}
	}
	if recs, err := store.Verifications(taskID); err == nil {
		for _, r := range recs {
			out = append(out, TimelineEntry{Timestamp: r.Timestamp, Kind: "verification", Summary: string(r.Status) + ": " + r.Claim, Detail: string(r.Surface)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}
