//go:build windows

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"leadin-helper/internal/testutil"
)

// Runs only on Windows (CI of the release): Kill stops cmd and the ping it
// started, through the job object.
func TestKillStopsTheChildThroughTheJob(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := "$p = Start-Process -PassThru -WindowStyle Hidden ping -ArgumentList '-n','60','127.0.0.1'; " +
		"Set-Content -NoNewline -Path '" + pidFile + "' -Value $p.Id; Start-Sleep 60"
	tree, err := Start(exec.Command("powershell", "-NoProfile", "-Command", script))
	if err != nil {
		t.Fatal(err)
	}
	child := waitForPid(t, pidFile)
	tree.Kill()
	select {
	case <-tree.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("main process still running")
	}
	deadline := time.Now().Add(5 * time.Second)
	for testutil.Alive(child) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d still alive", child)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestKillAfterExitDoesNothing(t *testing.T) {
	tree, err := Start(exec.Command("cmd", "/c", "exit 0"))
	if err != nil {
		t.Fatal(err)
	}
	<-tree.Done()
	tree.Kill()
	tree.Kill()
}

func waitForPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the child never started")
	return 0
}
