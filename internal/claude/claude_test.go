package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/G-DPP/leadin-helper/internal/testutil"
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

func TestBuildCommandMatchesTheEarlierHelper(t *testing.T) {
	got := BuildCommand("/x/claude", "opus", []string{"WebSearch", "WebFetch"}, "/run/mcp.json", "", nil)
	want := []string{
		"/x/claude", "-p", "--restricted", "--model", "opus",
		"--tools", "WebSearch,WebFetch",
		"--allowedTools", "mcp__leadin WebSearch WebFetch",
		"--mcp-config", "/run/mcp.json", "--strict-mcp-config", "--no-session-persistence",
		"--output-format", "stream-json", "--verbose", "--input-format", "stream-json",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("command = %q\nwant      %q", got, want)
	}
}

func TestBuildCommandWithSubagents(t *testing.T) {
	agents := `{"example-worker":{"description":"d","prompt":"p","tools":["WebSearch"]}}`
	got := BuildCommand("/x/claude", "sonnet", []string{"WebSearch", "WebFetch", "Agent"}, "/run/mcp.json", agents, nil)
	want := []string{
		"/x/claude", "-p", "--restricted", "--model", "sonnet",
		"--tools", "WebSearch,WebFetch,Agent",
		"--allowedTools", "mcp__leadin WebSearch WebFetch Agent",
		"--agents", agents,
		"--mcp-config", "/run/mcp.json", "--strict-mcp-config", "--no-session-persistence",
		"--output-format", "stream-json", "--verbose", "--input-format", "stream-json",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("command = %q\nwant      %q", got, want)
	}
}

func TestBuildCommandWithoutTools(t *testing.T) {
	got := BuildCommand("claude", "haiku", nil, "mcp.json", "", nil)
	if got[5] != "--tools" || got[6] != "" || got[8] != "mcp__leadin" {
		t.Fatalf("command = %q", got)
	}
}

func TestBuildCommandWithReadRules(t *testing.T) {
	rules := []string{"Read(~/.claude/projects/-tmp-run-work/**)"}
	got := BuildCommand("/x/claude", "sonnet", []string{"WebSearch", "WebFetch"}, "/run/config/mcp.json", "", rules)
	if got[6] != "WebSearch,WebFetch,Read" {
		t.Errorf("--tools = %q", got[6])
	}
	// The rule, never Read alone.
	if got[8] != "mcp__leadin WebSearch WebFetch "+rules[0] {
		t.Errorf("--allowedTools = %q", got[8])
	}
}

func TestReadRulesNameTheRunProjectFolders(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "work")
	if err := os.Mkdir(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	rules := ReadRules(workdir)
	dirs := ProjectDirs(workdir, "/c")
	if len(rules) != len(dirs) || len(rules) == 0 {
		t.Fatalf("rules = %q, project dirs = %q", rules, dirs)
	}
	// The same folders the end-of-run cleanup removes.
	for i, dir := range dirs {
		if want := "Read(~/.claude/projects/" + filepath.Base(dir) + "/**)"; rules[i] != want {
			t.Errorf("rule %d = %q, want %q", i, rules[i], want)
		}
	}
}

func TestRunEnvRaisesTheMCPOutputLimit(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	got := RunEnv(env)
	if !slices.Equal(got, []string{"PATH=/usr/bin", "MAX_MCP_OUTPUT_TOKENS=200000"}) {
		t.Fatalf("env = %q", got)
	}
	if len(env) != 1 {
		t.Fatalf("RunEnv changed its argument: %q", env)
	}
}

func TestMCPConfigIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteMCPConfig(dir, "https://leadin.example/mcp", "tok_secret")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "mcp.json") {
		t.Fatalf("path = %s", path)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", info.Mode(), err)
		}
	}
	data, _ := os.ReadFile(path)
	var config map[string]map[string]map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	server := config["mcpServers"]["leadin"]
	headers, _ := server["headers"].(map[string]any)
	if len(config["mcpServers"]) != 1 || server["type"] != "http" || server["url"] != "https://leadin.example/mcp" || headers["Authorization"] != "Bearer tok_secret" {
		t.Fatalf("config = %s", data)
	}
}

func TestConfigFilesMustNotExist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMCPConfig(dir, "https://leadin.example/mcp", "tok"); err == nil {
		t.Fatal("mcp.json overwritten")
	}
}

// The value of --agents is JSON, not a path: Claude Code 2.1.278 rejects a
// path.
func TestAgentsJSONIsTheGivenValue(t *testing.T) {
	agents := map[string]any{"example-worker": map[string]any{"description": "Finds <città> & more", "prompt": "p", "tools": []string{"WebSearch"}}}
	got, err := AgentsJSON(agents)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"example-worker":{"description":"Finds <città> & more","prompt":"p","tools":["WebSearch"]}}` {
		t.Fatalf("agents = %s", got)
	}
}

func TestRunDirNames(t *testing.T) {
	if got := dirName("/tmp/leadin-run-a1_b.c/è"); got != "-tmp-leadin-run-a1-b-c--" {
		t.Fatalf("dirName = %q", got)
	}
	workdir := t.TempDir()
	config := filepath.Join("home", ".claude")
	got := ProjectDirs(workdir, config)
	if !slices.Contains(got, filepath.Join(config, "projects", dirName(workdir))) {
		t.Fatalf("ProjectDirs = %v", got)
	}
}

func TestRunDirNamesFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	realPath, _ := filepath.EvalSymlinks(real)
	got := ProjectDirs(link, "cfg")
	want := []string{filepath.Join("cfg", "projects", dirName(link)), filepath.Join("cfg", "projects", dirName(realPath))}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("ProjectDirs = %v, want %v", got, want)
	}
	rules := ReadRules(link)
	wantRules := []string{"Read(~/.claude/projects/" + dirName(link) + "/**)", "Read(~/.claude/projects/" + dirName(realPath) + "/**)"}
	slices.Sort(wantRules)
	if !slices.Equal(rules, wantRules) {
		t.Fatalf("ReadRules = %q, want %q", rules, wantRules)
	}
	tasks := TaskOutputDirs(link)
	if len(tasks) != 2 || filepath.Base(filepath.Dir(tasks[0])) != "claude-"+strconv.Itoa(os.Getuid()) {
		t.Fatalf("TaskOutputDirs = %v", tasks)
	}
}

func TestRemoveRunDirsRemovesOnlyTheRunFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no task output folders on Windows")
	}
	t.Setenv("TMPDIR", t.TempDir())
	workdir, err := os.MkdirTemp("", "leadin-run-")
	if err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	dirs := append(ProjectDirs(workdir, config), TaskOutputDirs(workdir)...)
	other := filepath.Join(config, "projects", "-home-someone-project")
	for _, dir := range append(dirs, other) {
		if err := os.MkdirAll(filepath.Join(dir, "s", "tool-results"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	RemoveRunDirs(workdir, config)
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s still there", dir)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another project folder was removed: %v", err)
	}
}
