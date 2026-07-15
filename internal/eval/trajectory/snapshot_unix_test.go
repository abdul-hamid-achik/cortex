//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package trajectory

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSnapshotFilesRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "launcher.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotFiles(context.Background(), root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("fifo snapshot error = %v", err)
	}
}
