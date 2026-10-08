//go:build unix

package kernel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// detachedAttr starts the worker in its own session so it survives the MCP
// call (and the terminal) that queued it. Setsid makes the worker its own
// process-group leader, which terminateProcess relies on.
func detachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// terminateProcess asks a confirmed worker to stop. Because the worker was
// started with Setsid it leads its own process group, so the whole group
// (the worker plus any tool subprocesses) is signaled; when the pid does not
// lead a group of its own only the pid itself is signaled.
func terminateProcess(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
		return syscall.Kill(-pid, syscall.SIGTERM)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// processStartTime returns when the process started, to one-second precision.
// It reads /proc on Linux and falls back to ps(1), which is available on
// macOS and the BSDs. ok is false when the start time cannot be determined.
func processStartTime(ctx context.Context, pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	if t, ok := procFSStartTime(pid); ok {
		return t, true
	}
	return psStartTime(ctx, pid)
}

// procFSStartTime derives the start time from /proc/<pid>/stat (field 22,
// clock ticks since boot) and the boot time in /proc/stat.
func procFSStartTime(pid int) (time.Time, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304 -- fixed /proc path with a numeric pid
	if err != nil {
		return time.Time{}, false
	}
	// comm (field 2) may contain spaces and parentheses; split after the last ')'.
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(string(raw[end+1:]))
	// fields[0] is field 3 (state), so field 22 (starttime) is fields[19].
	if len(fields) < 20 {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil || ticks < 0 {
		return time.Time{}, false
	}
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(stat), "\n") {
		if rest, found := strings.CutPrefix(line, "btime "); found {
			boot, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil || boot <= 0 {
				return time.Time{}, false
			}
			// USER_HZ is 100 on every mainstream Linux ABI.
			return time.Unix(boot+ticks/100, 0), true
		}
	}
	return time.Time{}, false
}

func psStartTime(ctx context.Context, pid int) (time.Time, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid)) // #nosec G204 -- fixed argv, numeric pid
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	// "Tue Oct  7 10:12:33 2026"; Fields collapses the padded day.
	text := strings.Join(strings.Fields(string(out)), " ")
	if text == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", text, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
