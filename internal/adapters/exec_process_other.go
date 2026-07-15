//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package adapters

import "os/exec"

// configureProcessTree leaves CommandContext's direct-child cancellation in
// place on platforms without Unix process groups. WaitDelay still bounds any
// inherited stdout/stderr descriptors.
func configureProcessTree(*exec.Cmd) {}

func terminateProcessTree(*exec.Cmd) error { return nil }
