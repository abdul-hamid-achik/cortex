//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package trajectory

import (
	"os"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) {}

func processIsolationSupported() bool { return false }

func openSnapshotFile(path string) (*os.File, error) {
	return os.Open(path)
}
