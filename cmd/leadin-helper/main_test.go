package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestOriginArgumentActsAsNativeHost(t *testing.T) {
	binary := build(t)
	for _, args := range [][]string{
		{"chrome-extension://fmkimdbalkokmkkpkapnmjjohhhcdpbm/"},
		{"chrome-extension://fmkimdbalkokmkkpkapnmjjohhhcdpbm/", "--parent-window=0"},
	} {
		out, err := isolated(t, binary, args...).Output()
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if len(out) != 0 {
			t.Errorf("%q: with empty stdin the native host must not answer, got %q", args, out)
		}
	}
}

func TestUnknownArgumentsPrintUsage(t *testing.T) {
	binary := build(t)
	for _, args := range [][]string{{"--version", "--foo"}, {"--help"}, {"--install", "x"}} {
		cmd := isolated(t, binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(stderr.String(), "usage: leadin-helper") {
			t.Errorf("%q: err %v, stderr %q", args, err, stderr.String())
		}
	}
}

func TestInstallAndUninstallFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("writes the real HKCU keys of the browsers: covered by internal/install with test keys")
	}
	binary := build(t)
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+filepath.Join(home, "AppData"),
		"XDG_DATA_HOME=", "XDG_CONFIG_HOME="+filepath.Join(home, "config"))
	if runtime.GOOS == "linux" {
		if err := os.MkdirAll(filepath.Join(home, "config", "google-chrome"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS == "darwin" {
		if err := os.MkdirAll(filepath.Join(home, "Library", "Application Support", "Google", "Chrome"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, flag := range []string{"--install", "--uninstall"} {
		cmd := exec.Command(binary, flag)
		cmd.Env = env
		cmd.Stdin = bytes.NewReader(nil)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		want := map[string]string{"--install": "Installed: LeadIn helper 1.2.3 is ready.", "--uninstall": "Uninstalled:"}[flag]
		if !strings.Contains(string(out), want) || strings.Contains(string(out), "Press Enter") {
			t.Errorf("%s printed %s", flag, out)
		}
	}
}
