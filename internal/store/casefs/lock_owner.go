package casefs

import (
	"os"
	"strconv"
	"strings"
)

// lockOwnerAlive reads the PID written into a lock file and asks the host
// whether that process still exists. Malformed/zero PIDs are treated as dead so
// genuinely abandoned legacy locks remain recoverable.
func lockOwnerAlive(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "pid=")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(value)
		return err == nil && pid > 0 && processAlive(pid)
	}
	return false
}
