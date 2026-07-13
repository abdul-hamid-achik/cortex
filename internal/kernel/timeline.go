package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
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
// taskID and returns it opened. Use LocateSessionIn for a repo-local/custom
// store that is not visible to the central walk.
func LocateSession(taskID string) (string, *casefs.Store, error) {
	return LocateSessionIn("", taskID)
}

// LocateSessionIn finds taskID in the central session tree, then in the
// resolved case store for workspace. The explicit workspace fallback makes
// repo-local/custom cases_dir sessions addressable by MCP servers and CLI
// commands whose process cwd is not the task's repository.
func LocateSessionIn(workspace, taskID string) (string, *casefs.Store, error) {
	if slug, store, err := locateUnder(config.SessionsRoot(), taskID); err == nil {
		return slug, store, nil
	}
	// Fallback: a session kept repo-local (opt-in cases_dir, or a pre-existing
	// .cortex/cases that DefaultCasesDir honors) in the requested workspace —
	// the central-tree walk can't see those. Only probe an EXISTING dir so a
	// lookup never creates one.
	cfg := config.For(workspace)
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
// the metrics path.
func Timeline(taskID string) ([]TimelineEntry, error) {
	return TimelineIn("", taskID)
}

// TimelineIn returns a task timeline with an explicit workspace fallback for
// repo-local/custom case stores.
func TimelineIn(workspace, taskID string) ([]TimelineEntry, error) {
	_, store, err := LocateSessionIn(workspace, taskID)
	if err != nil {
		return nil, err
	}
	entries := timelineFromStore(store, taskID)
	if c, loadErr := store.Load(taskID); loadErr == nil {
		r := redact.New(config.For(c.Workspace.Root).RedactLiterals...)
		for i := range entries {
			entries[i].Summary = r.String(entries[i].Summary)
			entries[i].Detail = r.String(entries[i].Detail)
			entries[i].Ref = r.String(entries[i].Ref)
		}
	}
	return entries, nil
}

// timelineFromStore builds the merged, time-sorted feed for an already-located
// store — so callers that already hold the store (e.g. ShowSession) don't walk
// the tree twice.
func timelineFromStore(store *casefs.Store, taskID string) []TimelineEntry {
	snapshot, err := store.Snapshot(taskID)
	if err != nil {
		return nil
	}
	return timelineFromSnapshot(snapshot)
}

func timelineFromSnapshot(snapshot casefs.TaskSnapshot) []TimelineEntry {
	var out []TimelineEntry
	for _, e := range snapshot.PhaseEvents {
		out = append(out, TimelineEntry{Timestamp: e.Timestamp, Kind: "phase", Summary: string(e.From) + " → " + string(e.To)})
	}
	for _, e := range snapshot.Evidence {
		out = append(out, TimelineEntry{Timestamp: e.Timestamp, Kind: "evidence", Summary: e.Claim, Detail: string(e.Kind), Ref: e.ID})
	}
	for _, c := range snapshot.Commands {
		detail := c.ActionClass
		if c.Actor != "" {
			detail += " · " + c.Actor
		}
		out = append(out, TimelineEntry{Timestamp: c.Timestamp, Kind: "command", Summary: c.Tool + "." + c.Operation + " (" + c.Status + ")", Detail: detail})
	}
	for _, r := range snapshot.Verifications {
		out = append(out, TimelineEntry{Timestamp: r.Timestamp, Kind: "verification", Summary: string(r.Status) + ": " + r.Claim, Detail: string(r.Surface)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}
