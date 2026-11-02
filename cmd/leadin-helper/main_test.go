package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/G-DPP/leadin-helper/internal/version"
)

// build compiles the command with the version set as a release build does.
func build(t *testing.T) string {
	t.Helper()
	module, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "leadin-helper")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ldflags := fmt.Sprintf("-X %s/internal/version.Version=1.2.3", bytes.TrimSpace(module))
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}

// isolated runs the binary with an empty home, so the log never lands in the
// real one, and an empty stdin.
func isolated(t *testing.T, binary string, args ...string) *exec.Cmd {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+home, "XDG_DATA_HOME="+home)
	cmd.Stdin = bytes.NewReader(nil)
	return cmd
}

func TestVersionFlagPrintsVersion(t *testing.T) {
	binary := build(t)
	out, err := isolated(t, binary, "--version").Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	want := fmt.Sprintf("leadin-helper 1.2.3 (protocol %d, %s/%s)\n", version.ProtocolVersion, runtime.GOOS, runtime.GOARCH)
	if string(out) != want {
		t.Errorf("--version printed %q, want %q", out, want)
	}
}

func TestVersionWithOtherArgumentsActsAsNativeHost(t *testing.T) {
	binary := build(t)
	out, err := isolated(t, binary, "--version", "--foo").Output()
	if err != nil {
		t.Fatalf("native host with empty stdin: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("with empty stdin the native host must not answer, got %q", out)
	}
}
