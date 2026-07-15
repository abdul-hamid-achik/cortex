//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package trajectory

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		// Kill the command and descendants that remain in its process group.
		// Descendants that deliberately create a new session or process group are
		// outside this containment guarantee.
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
}

func processIsolationSupported() bool { return true }

func openSnapshotFile(path string) (*os.File, error) {
	// Non-blocking open ensures an untrusted launcher cannot leave a FIFO that
	// stalls the post-run snapshot before its file type is revalidated.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
