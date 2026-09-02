package casefs

import (
	"errors"
	"os"
	"path/filepath"
)

// maxCheckpointBytes bounds the resumable checkpoint document. It is meant to
// be injected into a model context after compaction, so it stays small.
const maxCheckpointBytes = 64 << 10

// WriteCheckpoint atomically replaces the task's checkpoint.md — the short,
// LLM-readable "what I know, what I am doing, what is next" packet that
// resume returns after context loss.
func (s *Store) WriteCheckpoint(taskID, md string) error {
	if err := ValidateTaskID(taskID); err != nil {
		return err
	}
	if len(md) > maxCheckpointBytes {
		md = md[:maxCheckpointBytes] + "\n…(checkpoint truncated)\n"
	}
	return writeFileAtomic(filepath.Join(s.dir(taskID), "checkpoint.md"), []byte(md), 0o600)
}

// ReadCheckpoint returns the last checkpoint, or "" when none was written.
func (s *Store) ReadCheckpoint(taskID string) (string, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return "", err
	}
	data, err := readFileLimited(filepath.Join(s.dir(taskID), "checkpoint.md"), maxCheckpointBytes+1024)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
