//go:build !unix

package kernel

import (
	"context"
	"os"
	"syscall"
	"time"
)

func detachedAttr() *syscall.SysProcAttr { return nil }

func terminateProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// processStartTime is unavailable on this platform, so worker identity is
// never confirmed and liveness falls back to the heartbeat alone.
func processStartTime(context.Context, int) (time.Time, bool) { return time.Time{}, false }
