//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package adapters

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessTree places the command in a new process group and replaces
// CommandContext's direct-child cancellation with a group-wide SIGKILL. A tool
// and every descendant that keeps the group therefore share one cancellation
// boundary.
func configureProcessTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		return terminateProcessTree(cmd)
	}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
