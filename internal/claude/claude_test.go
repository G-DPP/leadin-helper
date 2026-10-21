package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"leadin-helper/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.Run(m))
}

func pathEnv(dirs ...string) []string {
	return []string{"PATH=" + strings.Join(dirs, string(os.PathListSeparator))}
}

func TestFindPrefersPathOverKnownPaths(t *testing.T) {
	inPath := testutil.BuildFakeClaude(t)
	known := testutil.BuildFakeClaude(t)
	if got := find(pathEnv(filepath.Dir(inPath)), []string{known}); got != inPath {
		t.Fatalf("find = %q, want %q", got, inPath)
	}
}

func TestFindFallsBackToKnownPaths(t *testing.T) {
	known := testutil.BuildFakeClaude(t)
	missing := filepath.Join(t.TempDir(), testutil.ClaudeName())
	if got := find(pathEnv(t.TempDir()), []string{missing, known}); got != known {
		t.Fatalf("find = %q, want %q", got, known)
	}
}

func TestFindWithoutClaude(t *testing.T) {
	if got := find(pathEnv(t.TempDir()), []string{filepath.Join(t.TempDir(), "claude")}); got != "" {
		t.Fatalf("find = %q, want none", got)
	}
}

func TestFindSkipsNonExecutableFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := find(pathEnv(dir), nil); got != "" {
		t.Fatalf("find = %q, want none", got)
	}
}

func TestKnownPaths(t *testing.T) {
	home := filepath.Join("home", "someone")
	unix := knownPaths("linux", home, func(string) string { return "" })
	if len(unix) != 5 || unix[0] != filepath.Join(home, ".local", "bin", "claude") || !slices.Contains(unix, "/opt/homebrew/bin/claude") {
		t.Fatalf("unix known paths = %v", unix)
	}
	env := map[string]string{"USERPROFILE": "profile", "APPDATA": "appdata"}
	windows := knownPaths("windows", home, func(name string) string { return env[name] })
	want := []string{filepath.Join("profile", ".local", "bin", "claude.exe"), filepath.Join("appdata", "npm", "claude.cmd")}
	if !slices.Equal(windows, want) {
		t.Fatalf("windows known paths = %v, want %v", windows, want)
	}
}

func TestVersionIsTheFirstField(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Version: "2.1.300 (Claude Code)"})
	if got := Version(path, nil); got != "2.1.300" {
		t.Fatalf("Version = %q", got)
	}
}

func TestVersionOfAFailingClaudeIsEmpty(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	if got := Version(path, nil); got != "" {
		t.Fatalf("Version = %q, want empty", got)
	}
	if got := Version(filepath.Join(t.TempDir(), "missing"), nil); got != "" {
		t.Fatalf("Version of a missing claude = %q, want empty", got)
	}
}

func TestIsTooOld(t *testing.T) {
	cases := map[string]bool{
		"2.1.247":      true,
		"2.0.999":      true,
		"1.9.300":      true,
		"2.1.248":      false,
		"2.1.300":      false,
		"2.2.0":        false,
		"3.0.0":        false,
		"abc":          false,
		"":             false,
		"2.1":          false,
		"2.1.300-beta": false,
	}
	for version, want := range cases {
		if got := IsTooOld(version); got != want {
			t.Errorf("IsTooOld(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestAuthStatusLoggedIn(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Auth: `{"loggedIn": true, "subscriptionType": "max"}`})
	loggedIn, subscription := AuthStatus(path, nil)
	if !loggedIn || subscription == nil || *subscription != "max" {
		t.Fatalf("AuthStatus = %v, %v", loggedIn, subscription)
	}
}

func TestAuthStatusLoggedOut(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	// `claude auth status` exits 1 when logged out, and still prints JSON.
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Auth: `{"loggedIn": false}`, AuthExitCode: 1})
	loggedIn, subscription := AuthStatus(path, nil)
	if loggedIn || subscription != nil {
		t.Fatalf("AuthStatus = %v, %v", loggedIn, subscription)
	}
}

func TestAuthStatusWithInvalidJSONIsLoggedOut(t *testing.T) {
	for _, auth := range []string{"not json", `["loggedIn"]`, `{"loggedIn": "yes"}`} {
		path := testutil.BuildFakeClaude(t)
		testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Auth: auth})
		if loggedIn, _ := AuthStatus(path, nil); loggedIn {
			t.Errorf("%q: logged in", auth)
		}
	}
	if loggedIn, _ := AuthStatus(filepath.Join(t.TempDir(), "missing"), nil); loggedIn {
		t.Error("missing claude: logged in")
	}
}

func TestAuthStatusNeverReturnsTheEmail(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{
		Auth: `{"loggedIn": true, "email": "someone@example.com", "subscriptionType": "pro", "orgName": "Example"}`,
	})
	// The signature itself is the guarantee: only a bool and the subscription
	// type come out.
	loggedIn, subscription := AuthStatus(path, nil)
	if !loggedIn || subscription == nil || *subscription != "pro" {
		t.Fatalf("AuthStatus = %v, %v", loggedIn, subscription)
	}
}

func TestRunEnvironmentKeepsOnlyAllowedVariables(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/home/someone", "LC_ALL=it_IT.UTF-8", "HTTPS_PROXY=http://proxy:3128",
		"https_proxy=http://proxy:3128", "ANTHROPIC_API_KEY=secret", "ANTHROPIC_BASE_URL=https://example.com",
		"CLAUDE_CONFIG_DIR=/tmp/other", "CLAUDE_CODE_USE_BEDROCK=1", "FAKE_CLAUDE_X=1", "AWS_SECRET_ACCESS_KEY=secret",
		"=C:=C:\\", "NOEQUALS",
	}
	got := runEnvironment(environ, false)
	want := []string{"PATH=/usr/bin", "HOME=/home/someone", "LC_ALL=it_IT.UTF-8", "HTTPS_PROXY=http://proxy:3128", "https_proxy=http://proxy:3128"}
	if !slices.Equal(got, want) {
		t.Fatalf("runEnvironment = %v, want %v", got, want)
	}
}

func TestRunEnvironmentIsCaseInsensitiveOnWindows(t *testing.T) {
	environ := []string{"Path=C:\\Windows", "SYSTEMROOT=C:\\Windows", "lc_all=C", "Anthropic_Api_Key=secret", "Claude_Config_Dir=x"}
	got := runEnvironment(environ, true)
	want := []string{"Path=C:\\Windows", "SYSTEMROOT=C:\\Windows", "lc_all=C"}
	if !slices.Equal(got, want) {
		t.Fatalf("runEnvironment = %v, want %v", got, want)
	}
	if got := runEnvironment(environ, false); len(got) != 0 {
		t.Fatalf("case-sensitive runEnvironment = %v, want none", got)
	}
}

func TestApiKeyNeverReachesAuthStatus(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	recordPath := filepath.Join(t.TempDir(), "calls.jsonl")
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Auth: `{"loggedIn": true}`, Record: recordPath})
	environ := []string{
		"PATH=" + filepath.Dir(path), "ANTHROPIC_API_KEY=secret", "CLAUDE_CONFIG_DIR=/tmp/other", "FAKE_CLAUDE_X=1", "LC_ALL=C",
	}
	AuthStatus(path, RunEnvironment(environ))
	calls := testutil.ReadCalls(t, recordPath)
	if len(calls) != 1 || !slices.Equal(calls[0].Args, []string{"auth", "status"}) {
		t.Fatalf("calls = %+v", calls)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR", "FAKE_CLAUDE_X"} {
		if slices.Contains(calls[0].Env, name) {
			t.Errorf("%s reached claude: %v", name, calls[0].Env)
		}
	}
	if !slices.Contains(calls[0].Env, "LC_ALL") {
		t.Errorf("LC_ALL did not reach claude: %v", calls[0].Env)
	}
}
