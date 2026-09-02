//go:build !unix

package kernel

import (
	"os"
	"syscall"
)

func detachedAttr() *syscall.SysProcAttr { return nil }

func terminateProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
