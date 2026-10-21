package run

// Each test below is one of the guarantees listed in README.md. If one fails,
// the guarantee no longer holds.
//
// Every test runs the whole start_run (fake server and fake claude) and
// checks the guarantee from the outside: what claude received, what was left
// on disk, what the browser and the log got.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// assertRejected checks that the spec edited by edit ends in invalid_spec
// and that claude never ran, not even for --version.
func assertRejected(t *testing.T, name string, edit func(map[string]any)) {
	t.Helper()
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	edit(e.server.spec)
	r := e.run(0)
	assertCode(t, r, "invalid_spec")
	if len(r.events) != 1 || len(e.calls()) != 0 {
		t.Fatalf("%s: events %v, calls %v", name, r.events, e.calls())
	}
}

// agentTools edits the tools of the subagent of the spec.
func agentTools(tools ...any) func(map[string]any) {
	return func(s map[string]any) {
		s["agents"] = map[string]any{"example-worker": map[string]any{
			"description": "A made-up subagent.",
			"prompt":      "Call `start_group` first.",
			"tools":       tools,
		}}
	}
}

// assertRunGone checks that the temporary folder of the run and the folders
// Claude Code made for it are gone.
func (e *env) assertRunGone() {
	e.t.Helper()
	if _, err := os.Stat(e.runCall().Cwd); !os.IsNotExist(err) {
		e.t.Fatalf("the temporary folder is still there: %v", err)
	}
	e.assertNoLeftovers()
}

func TestRunsInEmptyTemporaryDirectory(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.run(0)
	call := e.runCall()
	if !strings.HasPrefix(filepath.Base(call.Cwd), "leadin-run-") || filepath.Dir(call.Cwd) != e.tmp {
		t.Fatalf("cwd = %s", call.Cwd)
	}
	if !slices.Equal(call.Files, []string{"mcp.json"}) {
		t.Fatalf("files = %v", call.Files)
	}

	e = newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	e.run(0)
	if files := e.runCall().Files; !slices.Equal(files, []string{"agents.json", "mcp.json"}) {
		t.Fatalf("files with subagents = %v", files)
	}
}

func TestTemporaryDirectoryIsRemovedAfterSuccess(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Project = true
	if r := e.run(0); r.last()["type"] != "run_finished" {
		t.Fatalf("events = %v", r.events)
	}
	e.assertRunGone()
}

func TestTemporaryDirectoryIsRemovedAfterFailure(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Project = true
	e.config.Fixture = fixture(t, "run_mcp_failed.jsonl")
	assertCode(t, e.run(0), "mcp_unauthorized")
	e.assertRunGone()
}

func TestTemporaryDirectoryIsRemovedAfterCancel(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Project = true
	e.config.DelayMS = 1000
	if r := e.run(1500 * time.Millisecond); r.fields["outcome"] != "cancelled" {
		t.Fatalf("fields = %v", r.fields)
	}
	e.assertRunGone()
}

func TestTemporaryDirectoryIsRemovedAfterTimeout(t *testing.T) {
	skipOnWindows(t)
	maxRunDuration = time.Second
	t.Cleanup(func() { maxRunDuration = MaxRunDuration })
	e := newEnv(t)
	e.config.Project = true
	e.config.DelayMS = 1000
	assertCode(t, e.run(0), "timeout")
	e.assertRunGone()
}

func TestTemporaryDirectoryIsRemovedAfterPanic(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Project = true
	e.config.DelayMS = 1000
	e.config.Child = filepath.Join(t.TempDir(), "child.pid")
	// The panic comes once claude has made its folders and started a child.
	afterStart = func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(e.config.Child); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		panic("injected")
	}
	t.Cleanup(func() { afterStart = func() {} })
	recovered := func() (value any) {
		defer func() { value = recover() }()
		e.run(0)
		return nil
	}()
	if recovered != "injected" {
		t.Fatalf("recovered = %v", recovered)
	}
	// claude had started: it is stopped, and its folders are removed.
	assertDead(t, readPid(t, e.config.Child))
	e.assertRunGone()
}

func TestOnlyAllowedBuiltinToolsReachClaude(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.server.spec["tools"] = []any{"WebFetch", "Agent"}
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	e.run(0)
	call := e.runCall()
	// Only the spec's tools, among WebSearch, WebFetch and Agent.
	tools := strings.Split(argAfter(call.Args, "--tools"), ",")
	if !slices.Equal(tools, []string{"WebFetch", "Agent"}) {
		t.Fatalf("--tools = %v", tools)
	}
	if allowed := argAfter(call.Args, "--allowedTools"); allowed != "mcp__leadin WebFetch Agent" {
		t.Fatalf("--allowedTools = %q", allowed)
	}
}

func TestSpecWithBashIsRejected(t *testing.T) {
	assertRejected(t, "Bash", func(s map[string]any) { s["tools"] = []any{"WebSearch", "Bash"} })
}

func TestSpecWithReadWriteEditIsRejected(t *testing.T) {
	for _, tool := range []string{"Read", "Write", "Edit", "Glob", "Grep", "NotebookEdit", "mcp__leadin__store_item"} {
		assertRejected(t, tool, func(s map[string]any) { s["tools"] = []any{"WebSearch", tool} })
	}
}

func TestSubagentToolsAreLimited(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"Bash":             agentTools("WebSearch", "Bash"),
		"Read":             agentTools("Read"),
		"other MCP server": agentTools("mcp__other__tool"),
		"no tools":         agentTools(),
		"hooks": func(s map[string]any) {
			agentTools("WebSearch")(s)
			s["agents"].(map[string]any)["example-worker"].(map[string]any)["hooks"] = map[string]any{}
		},
		"mcpServers": func(s map[string]any) {
			agentTools("WebSearch")(s)
			s["agents"].(map[string]any)["example-worker"].(map[string]any)["mcpServers"] = map[string]any{}
		},
	} {
		assertRejected(t, name, edit)
	}
}

func TestSubagentWithAgentToolIsRejected(t *testing.T) {
	assertRejected(t, "Agent", agentTools("WebSearch", "Agent"))
}

func TestOnlyLeadinMCPServerIsConfigured(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.run(0)
	call := e.runCall()
	if !slices.Contains(call.Args, "--strict-mcp-config") {
		t.Fatalf("args = %v", call.Args)
	}
	var config map[string]map[string]any
	if err := json.Unmarshal([]byte(call.MCPConfig), &config); err != nil {
		t.Fatal(err)
	}
	if len(config) != 1 || len(config["mcpServers"]) != 1 || config["mcpServers"]["leadin"] == nil {
		t.Fatalf("mcp.json = %s", call.MCPConfig)
	}
}

func TestMCPOnOtherOriginIsRejected(t *testing.T) {
	for _, mcpURL := range []string{
		"https://leadin.example/mcp",
		"http://127.0.0.1:1/mcp",
		"http://localhost.example/mcp",
	} {
		assertRejected(t, mcpURL, func(s map[string]any) { s["mcp_url"] = mcpURL })
	}
	// Same host and port, another scheme.
	assertRejected(t, "https", func(s map[string]any) {
		s["mcp_url"] = strings.Replace(s["mcp_url"].(string), "http://", "https://", 1)
	})
}

func TestPlainHTTPRemoteMCPIsRejected(t *testing.T) {
	// The MCP server is on the LeadIn server's origin, and the extension
	// cannot point the helper to a remote server over http.
	for _, serverURL := range []string{"http://leadin.example", "http://192.0.2.1:8000"} {
		if _, ok := ParseStart(startMessage(serverURL, nil)); ok {
			t.Errorf("%s accepted", serverURL)
		}
	}
	assertRejected(t, "http remote", func(s map[string]any) { s["mcp_url"] = "http://leadin.example/mcp" })
}

func TestPromptIsNeverInArguments(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.run(0)
	call := e.runCall()
	arguments := strings.Join(call.Args, " ")
	for _, part := range []string{"start_example", "example.com/item", "follow the instructions"} {
		if strings.Contains(arguments, part) {
			t.Errorf("%q in the arguments", part)
		}
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(call.Stdin), &message); err != nil {
		t.Fatal(err)
	}
	if message["message"].(map[string]any)["content"] != testPrompt {
		t.Fatalf("stdin = %q", call.Stdin)
	}
}

func TestTokenIsNeverInArguments(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	e.run(0)
	call := e.runCall()
	arguments := strings.Join(call.Args, " ")
	for _, secret := range []string{testToken, testTicket, "Bearer"} {
		if strings.Contains(arguments, secret) {
			t.Errorf("%q in the arguments", secret)
		}
	}
	for _, name := range call.Env {
		if strings.Contains(name, testToken) {
			t.Error("the token in the environment")
		}
	}
	if !strings.Contains(call.MCPConfig, "Bearer "+testToken) {
		t.Fatalf("mcp.json = %s", call.MCPConfig)
	}
}

func TestTokenFileIsOwnerOnly(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	e.run(0)
	if mode := e.runCall().MCPMode; mode != "600" {
		t.Fatalf("mcp.json mode = %s", mode)
	}
}

func TestSessionIsNotPersisted(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.run(0)
	if args := e.runCall().Args; !slices.Contains(args, "--no-session-persistence") {
		t.Fatalf("args = %v", args)
	}
}

func TestPersonalConfigurationIsExcluded(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_SAFE_MODE", "ANYTHING"} {
		t.Setenv(name, "1")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.home, "other-config"))
	if r := e.run(0); r.last()["type"] != "run_finished" {
		t.Fatalf("events = %v", r.events)
	}
	call := e.runCall()
	if !slices.Equal(call.Args[:2], []string{"-p", "--restricted"}) || !slices.Contains(call.Args, "--strict-mcp-config") {
		t.Fatalf("args = %v", call.Args)
	}
	for _, name := range call.Env {
		if strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_") || name == "ANYTHING" {
			t.Errorf("%s reached claude", name)
		}
	}
	if !slices.Contains(call.Env, "HOME") {
		t.Fatalf("env = %v", call.Env)
	}
}

func TestRunCannotOutliveMaxDuration(t *testing.T) {
	skipOnWindows(t)
	if MaxRunDuration != 3*time.Hour {
		t.Fatalf("MaxRunDuration = %v, the README says 3 hours", MaxRunDuration)
	}
	maxRunDuration = time.Second
	t.Cleanup(func() { maxRunDuration = MaxRunDuration })
	e := newEnv(t)
	// Unknown fields are ignored: none of them can lengthen the run.
	e.server.spec["timeout"] = 86400
	e.server.spec["max_run_duration"] = 86400
	e.config.DelayMS = 1000
	e.config.Child = filepath.Join(t.TempDir(), "child.pid")
	started := time.Now()
	r := e.run(0)
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("the run lasted %v", elapsed)
	}
	assertCode(t, r, "timeout")
	assertDead(t, readPid(t, e.config.Child))
}

func TestLogHasNoPromptTokenOrToolNames(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.server.spec["prompt"] = "Call `start_example`.\n\nZanzibarite safety item"
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	r := e.run(0)
	if !strings.Contains(r.log, "action=claude_start run_id=7") || !strings.Contains(r.log, "key=group_done") {
		t.Fatalf("log = %q", r.log)
	}
	for _, secret := range []string{
		"Zanzibarite", "Call", testToken, testTicket, e.server.URL, "127.0.0.1",
		"start_example", "start_group", "store_item", "mcp__", "WebSearch", "WebFetch", "Agent", "example-worker",
		"Stored.", "someone@example.com",
	} {
		if strings.Contains(r.log, secret) {
			t.Errorf("%q in the log", secret)
		}
	}
	// Not towards the browser either: only keys and counters.
	for _, event := range r.progress() {
		if !slices.Equal(keys(event), []string{"count", "key", "type"}) {
			t.Fatalf("event = %v", event)
		}
	}
}

func TestRunDirectoriesAreRemoved(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Project = true
	otherProject := filepath.Join(e.home, ".claude", "projects", "-home-someone-other-project")
	otherTasks := filepath.Join(e.tmp, "claude-"+strconv.Itoa(os.Getuid()), "-home-someone-other-project")
	for _, dir := range []string{otherProject, otherTasks} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.run(0)
	for _, parent := range []string{filepath.Dir(otherProject), filepath.Dir(otherTasks)} {
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		// The run's folder is gone, the other project's is still there.
		if !slices.Equal(names, []string{"-home-someone-other-project"}) {
			t.Fatalf("%s = %v", parent, names)
		}
	}
}
