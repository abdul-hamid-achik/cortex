//go:build unix

/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

// processGroupsSupported reports whether the launcher can contain the agent
// in its own process group.
const processGroupsSupported = true

func setProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the whole group led by pid. A group that
// is already gone is not an error.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
