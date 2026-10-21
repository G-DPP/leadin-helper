//go:build windows

package proc

// Windows: for now only the main process is stopped. The job object that
// stops its children too comes with the Windows sprint.

import (
	"os/exec"
	"syscall"
	"time"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup | createNoWindow}
}

type osTree struct{}

func attach(*exec.Cmd) osTree { return osTree{} }

func (osTree) kill(cmd *exec.Cmd, done <-chan struct{}, _ time.Duration) {
	select {
	case <-done:
		return
	default:
	}
	_ = cmd.Process.Kill()
	<-done
}
