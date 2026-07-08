package ids

import (
	"strings"
	"testing"
	"time"
)

func TestNewHasPrefix(t *testing.T) {
	id := New("task")
	if !strings.HasPrefix(id, "task_") {
		t.Errorf("id %q missing prefix", id)
	}
	if len(id) <= len("task_") {
		t.Errorf("id %q has no body", id)
	}
}

func TestNewUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		id := New("ev")
		if seen[id] {
			t.Fatalf("duplicate id generated: %q", id)
		}
		seen[id] = true
	}
}

func TestNewTimeSortable(t *testing.T) {
	// IDs minted later must sort lexically after earlier ones (same prefix).
	orig := now
	defer func() { now = orig }()

	now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	early := New("task")
	now = func() time.Time { return time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC) }
	late := New("task")

	if early >= late {
		t.Errorf("expected %q < %q (time-sortable)", early, late)
	}
}
