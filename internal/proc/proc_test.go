//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"leadin-helper/internal/testutil"
)

func TestKillStopsAChildThatIgnoresSIGTERM(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// The ignored SIGTERM is inherited by sleep too.
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 30 & echo $! > "+pidFile+"; wait")
	tree, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	child := waitForPid(t, pidFile)
	started := time.Now()
	tree.Kill()
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("Kill took %v", elapsed)
	}
	select {
	case <-tree.Done():
	case <-time.After(time.Second):
		t.Fatal("main process still running")
	}
	waitUntilGone(t, child)
}

func TestKillStopsTheTreeAtOnceOnSIGTERM(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	tree, err := Start(exec.Command("sh", "-c", "sleep 30 & echo $! > "+pidFile+"; wait"))
	if err != nil {
		t.Fatal(err)
	}
	child := waitForPid(t, pidFile)
	started := time.Now()
	tree.Kill()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Kill took %v", elapsed)
	}
	waitUntilGone(t, child)
}

func TestKillAfterExitDoesNothing(t *testing.T) {
	tree, err := Start(exec.Command("true"))
	if err != nil {
		t.Fatal(err)
	}
	<-tree.Done()
	if !tree.Exited() {
		t.Fatal("Exited is false after Done")
	}
	tree.Kill()
	tree.Kill()
}

func waitForPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(data), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the child never started")
	return 0
}

// waitUntilGone fails unless pid disappears (or becomes a zombie, reaped by
// init) within a second.
func waitUntilGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !testutil.Alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("child %d still alive", pid)
}
