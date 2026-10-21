// Package proc starts claude in a process tree of its own and stops the
// whole tree: claude and every child it started.
package proc

import (
	"os/exec"
	"sync"
	"time"
)

// KillGrace is the wait between the polite stop and the forced one.
const KillGrace = 3 * time.Second

// Tree is a started process with its children.
type Tree struct {
	cmd    *exec.Cmd
	done   chan struct{}
	killMu sync.Mutex
	os     osTree
}

// Start starts cmd in a process tree of its own. The process is waited for
// in the background: Done is closed when it has exited. cmd.Stdout and
// cmd.Stdin, if set, should be *os.File (or pipes from cmd), so waiting does
// not depend on copying them.
func Start(cmd *exec.Cmd) (*Tree, error) {
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tree := &Tree{cmd: cmd, done: make(chan struct{})}
	tree.os = attach(cmd)
	go func() {
		_ = cmd.Wait()
		close(tree.done)
	}()
	return tree, nil
}

// Done is closed when the main process has exited and been waited for.
func (t *Tree) Done() <-chan struct{} { return t.done }

// Exited reports whether the main process has exited.
func (t *Tree) Exited() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// Kill stops the main process and all its children. It does nothing if they
// have already exited, and it is safe to call more than once.
func (t *Tree) Kill() {
	t.killMu.Lock()
	defer t.killMu.Unlock()
	t.os.kill(t.cmd, t.done, KillGrace)
}
