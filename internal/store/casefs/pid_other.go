//go:build !unix && !windows

package casefs

// Unsupported release targets fail closed rather than stealing a lock from a
// process whose liveness cannot be queried portably.
func processAlive(pid int) bool { return pid > 0 }
