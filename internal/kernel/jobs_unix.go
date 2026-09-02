//go:build unix

package kernel

import (
	"os"
	"syscall"
)

// detachedAttr starts the worker in its own session so it survives the MCP
// call (and the terminal) that queued it.
func detachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func terminateProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
