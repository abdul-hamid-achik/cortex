package kernel

import (
	"fmt"

	"github.com/abdul-hamid-achik/cortex/internal/config"
)

// DeleteSession permanently removes a session's directory. It locates the
// session in the active tree first, then the archive. It refuses in-flight
// (non-terminal) sessions regardless of apply. With apply=false it is a dry run:
// it returns the directory that WOULD be deleted without removing anything.
// Destructive and irreversible when apply=true.
func DeleteSession(taskID string, apply bool) (string, error) {
	_, store, err := LocateSession(taskID)
	if err != nil {
		_, store, err = locateUnder(config.ArchiveRoot(), taskID)
		if err != nil {
			return "", err
		}
	}
	c, err := store.Load(taskID)
	if err != nil {
		return "", err
	}
	if !c.Status.IsTerminal() {
		return "", fmt.Errorf("refusing to delete an in-flight session (phase %s) — complete, abort, or archive it first", c.Status)
	}
	path, err := store.TaskDir(taskID)
	if err != nil {
		return "", err
	}
	if !apply {
		return path, nil
	}
	if err := store.RemoveTask(taskID); err != nil {
		return "", err
	}
	return path, nil
}
