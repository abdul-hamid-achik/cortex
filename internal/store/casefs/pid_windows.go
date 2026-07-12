//go:build windows

package casefs

import (
	"errors"

	"golang.org/x/sys/windows"
)

func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied still establishes that the process exists.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		// Fail closed: an unqueryable live handle must not permit lock stealing.
		return true
	}
	const stillActive = 259
	return exitCode == stillActive
}
