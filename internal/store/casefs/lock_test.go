package casefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withFastLockTiming(t *testing.T) {
	t.Helper()
	oldWait, oldStale, oldHeartbeat := lockWait, lockStale, lockHeartbeat
	lockWait = 80 * time.Millisecond
	lockStale = 30 * time.Millisecond
	lockHeartbeat = 5 * time.Millisecond
	t.Cleanup(func() {
		lockWait, lockStale, lockHeartbeat = oldWait, oldStale, oldHeartbeat
	})
}

func TestTaskLockHeartbeatPreventsLiveOwnerReaping(t *testing.T) {
	withFastLockTiming(t)
	root := t.TempDir()
	first, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- first.withTaskLock("task_live", func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	secondEntered := false
	err = second.withTaskLock("task_live", func() error {
		secondEntered = true
		return nil
	})
	if !errors.Is(err, ErrBusy) || secondEntered {
		t.Fatalf("live lock was reaped: entered=%v err=%v", secondEntered, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockReapsCrashedOwner(t *testing.T) {
	withFastLockTiming(t)
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, ".task_crashed.lock")
	if err := os.WriteFile(lockPath, []byte("pid=0\ntoken=dead\nheartbeat=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	entered := false
	if err := store.withTaskLock("task_crashed", func() error {
		entered = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !entered {
		t.Fatal("stale lock was not recovered")
	}
}

func TestTaskLockDoesNotReapStaleButLiveOwner(t *testing.T) {
	withFastLockTiming(t)
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, ".task_suspended.lock")
	content := fmt.Sprintf("pid=%d\ntoken=live\nheartbeat=0\n", os.Getpid())
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	entered := false
	err = store.withTaskLock("task_suspended", func() error {
		entered = true
		return nil
	})
	if !errors.Is(err, ErrBusy) || entered {
		t.Fatalf("stale live owner was reaped: entered=%v err=%v", entered, err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("live owner's lock was removed: %v", err)
	}
}

func TestTaskLockReleaseDoesNotRemoveReplacementOwner(t *testing.T) {
	withFastLockTiming(t)
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, ".task_replaced.lock")
	if err := store.withTaskLock("task_replaced", func() error {
		if err := os.Remove(lockPath); err != nil {
			return err
		}
		return os.WriteFile(lockPath, []byte("pid=2\ntoken=foreign\nheartbeat=0\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("replacement lock was removed: %v", err)
	}
	if string(data) != "pid=2\ntoken=foreign\nheartbeat=0\n" {
		t.Fatalf("replacement lock changed: %q", data)
	}
}
