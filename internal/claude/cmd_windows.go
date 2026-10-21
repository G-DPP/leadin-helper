//go:build windows

package claude

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: claude is a console program, and
// without it each short command would flash a console window.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
