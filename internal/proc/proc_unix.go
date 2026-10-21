//go:build !windows

package proc

// Unix: the process leads a process group of its own (Setpgid), inherited by
// every child. Kill sends SIGTERM to the group and SIGKILL after the grace
// period, like kill_tree of the earlier Python helper.

import (
	"os/exec"
	"syscall"
	"time"
)

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

type osTree struct{ pgid int }

func attach(cmd *exec.Cmd) osTree {
	return osTree{pgid: cmd.Process.Pid}
}

// kill stops the group. done is closed once the main process has been waited
// for: until then its zombie keeps the group alive.
func (t osTree) kill(_ *exec.Cmd, done <-chan struct{}, grace time.Duration) {
	if !t.alive() {
		return
	}
	_ = syscall.Kill(-t.pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			if !t.alive() {
				return
			}
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
}

// release has nothing to free: the group goes away with its processes.
func (osTree) release() {}

// alive is true while at least one process is in the group.
func (t osTree) alive() bool {
	err := syscall.Kill(-t.pgid, 0)
	return err == nil || err == syscall.EPERM
}
