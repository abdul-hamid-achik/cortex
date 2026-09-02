package casefs

// ProcessAlive reports whether a pid still names a live process. Detached job
// liveness uses it so a dead worker is reported as failed, not running.
func ProcessAlive(pid int) bool { return processAlive(pid) }
