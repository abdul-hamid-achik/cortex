package kernel

import (
	"fmt"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/config"
)

// ArchiveSession retires a terminal session: it MOVES the case directory out of
// the active tree into config.ArchiveRoot (data preserved, reversible with
// UnarchiveSession), keeping `cortex sessions`/`overview` focused on live work.
// It refuses in-flight sessions — archiving active work would hide it. Returns
// the repo slug the session was archived under.
func ArchiveSession(taskID string) (string, error) {
	slug, store, err := LocateSession(taskID)
	if err != nil {
		return "", err
	}
	c, err := store.Load(taskID)
	if err != nil {
		return "", err
	}
	if !c.Status.IsTerminal() {
		return "", fmt.Errorf("refusing to archive an in-flight session (phase %s) — complete or abort it first", c.Status)
	}
	if err := store.MoveTaskTo(taskID, filepath.Join(config.ArchiveRoot(), slug)); err != nil {
		return "", err
	}
	return slug, nil
}

// UnarchiveSession moves an archived session back into the active tree.
func UnarchiveSession(taskID string) (string, error) {
	slug, store, err := locateUnder(config.ArchiveRoot(), taskID)
	if err != nil {
		return "", err
	}
	if err := store.MoveTaskTo(taskID, filepath.Join(config.SessionsRoot(), slug)); err != nil {
		return "", err
	}
	return slug, nil
}
