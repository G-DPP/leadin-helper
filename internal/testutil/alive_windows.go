//go:build windows

package testutil

import "syscall"

// stillActive is STILL_ACTIVE, the exit code of a running process.
const stillActive = 259

// Alive reports whether pid is still running.
func Alive(pid int) bool {
	const queryLimitedInformation = 0x1000
	handle, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
