//go:build !windows

package testutil

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Alive reports whether pid exists and is not a zombie.
func Alive(pid int) bool {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		// The state follows the last ")" of the command name.
		text := string(data)
		fields := strings.Fields(text[strings.LastIndex(text, ")")+1:])
		return len(fields) > 0 && fields[0] != "Z"
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
