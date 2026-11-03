package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/protocol"
	"github.com/G-DPP/leadin-helper/internal/testutil"
	"github.com/G-DPP/leadin-helper/internal/version"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.Run(m))
}

const testToken = "tok_" + "example" + "0123456789abcdef"

const testPrompt = "Call `start_example` and follow the instructions it returns.\n\nhttps://example.com/item"

// The progress map, outcomes and result id key of a made-up spec, matching
// the fixtures of package stream.
var testProgressMap = map[string]any{
	"start_example":  "started",
	"store_item":     "item_saved",
	"start_group":    "group_started",
	"close_group":    "group_done",
	"store_summary":  "summary_saved",
	"finish_example": "completing",
	"WebSearch":      "searching_web",
	"WebFetch":       "reading_page",
}

var testAgents = map[string]any{
	"example-worker": map[string]any{
		"description": "A made-up subagent.",
		"prompt":      "Call `start_group` first and follow the instructions it returns.",
		"tools":       []any{"WebSearch", "WebFetch", "mcp__leadin__*"},
	},
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "stream", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// redeemServer is a fake LeadIn backend for the redeem: it answers every
// POST with status and the body of spec, and records the requests.
type redeemServer struct {
	*httptest.Server
	mu       sync.Mutex
	status   int
	spec     map[string]any
	raw      string
	requests []map[string]any
	paths    []string
}

func newRedeemServer(t *testing.T) *redeemServer {
	t.Helper()
	server := &redeemServer{status: http.StatusOK}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		server.mu.Lock()
		server.requests = append(server.requests, body)
		server.paths = append(server.paths, r.URL.Path)
		status, payload := server.status, server.raw
		if payload == "" {
			encoded, _ := json.Marshal(server.spec)
			payload = string(encoded)
		}
		server.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(server.Close)
	server.spec = makeSpec(server.URL, false)
	return server
}

// makeSpec is a valid spec for serverURL; with agents it has a subagent.
func makeSpec(serverURL string, agents bool) map[string]any {
	spec := map[string]any{
		"run_id":          7,
		"mcp_url":         serverURL + "/mcp",
		"mcp_token":       testToken,
		"model":           "sonnet",
		"prompt":          testPrompt,
		"tools":           []any{"WebSearch", "WebFetch"},
		"progress_map":    testProgressMap,
		"outcomes":        []any{"done", "skipped", "failed"},
		"result_id_key":   "item_id",
		"failure_message": "The run did not finish correctly.",
	}
	if agents {
		spec["tools"] = []any{"WebSearch", "WebFetch", "Agent"}
		spec["agents"] = testAgents
	}
	return spec
}

// env is the isolated world of one test: an empty home, a TMPDIR of its
// own, a PATH with only the fake claude and a redeem server.
type env struct {
	t      *testing.T
	home   string
	tmp    string
	claude string
	record string
	config testutil.FakeConfig
	server *redeemServer
	log    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	// The real path of the temporary folder: on macOS it is under /var, a
	// symlink to /private/var, and the run would get two names (the Read
	// rules, the project folders). The symlink case has its own tests in
	// internal/claude.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, home: t.TempDir(), tmp: tmp}
	e.claude = testutil.BuildFakeClaude(t)
	e.record = filepath.Join(t.TempDir(), "calls.jsonl")
	e.log = filepath.Join(t.TempDir(), "helper.log")
	t.Setenv("HOME", e.home)
	t.Setenv("USERPROFILE", e.home)
	t.Setenv("APPDATA", "")
	t.Setenv("PATH", filepath.Dir(e.claude))
	t.Setenv("TMPDIR", e.tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	e.config = testutil.FakeConfig{
		Version: "2.1.300 (Claude Code)",
		Auth:    `{"loggedIn": true, "email": "someone@example.com", "subscriptionType": "pro"}`,
		Record:  e.record,
		Fixture: fixture(t, "run_basic.jsonl"),
	}
	e.server = newRedeemServer(t)
	return e
}

// result is what one run produced.
type result struct {
	events []map[string]any
	fields map[string]any
	log    string
}

func (r result) types() []string {
	types := []string{}
	for _, event := range r.events {
		types = append(types, event["type"].(string))
	}
	return types
}

func (r result) last() map[string]any {
	if len(r.events) == 0 {
		return nil
	}
	return r.events[len(r.events)-1]
}

func (r result) progress() []map[string]any {
	var events []map[string]any
	for _, event := range r.events {
		if event["type"] == "run_progress" {
			events = append(events, event)
		}
	}
	return events
}

// run executes a run with stdin on a pipe that stays open until the run
// ends, or is closed after closeAfter (the extension closing the port).
func (e *env) run(closeAfter time.Duration) result {
	e.t.Helper()
	testutil.WriteFakeConfig(e.t, e.claude, e.config)
	stdinRead, stdinWrite := io.Pipe()
	defer stdinWrite.Close()
	if closeAfter > 0 {
		timer := time.AfterFunc(closeAfter, func() { stdinWrite.Close() })
		defer timer.Stop()
	}
	request, ok := ParseStart(startMessage(e.server.URL, nil))
	if !ok {
		e.t.Fatal("invalid start request")
	}
	var out bytes.Buffer
	fields := Run(request, stdinRead, &out, logs.New(e.log))
	var events []map[string]any
	for {
		message, err := protocol.ReadMessage(&out)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			e.t.Fatalf("reading the events: %v", err)
		}
		events = append(events, message)
	}
	data, _ := os.ReadFile(e.log)
	return result{events: events, fields: fields, log: string(data)}
}

// calls are the recorded invocations of the fake claude.
func (e *env) calls() []testutil.Call {
	e.t.Helper()
	if _, err := os.Stat(e.record); os.IsNotExist(err) {
		return nil
	}
	return testutil.ReadCalls(e.t, e.record)
}

// runCall is the recorded `claude -p`.
func (e *env) runCall() testutil.Call {
	e.t.Helper()
	for _, call := range e.calls() {
		if len(call.Args) > 0 && call.Args[0] == "-p" {
			return call
		}
	}
	e.t.Fatal("claude -p never ran")
	return testutil.Call{}
}

// dirName is the folder name Claude Code derives from a path (as in package
// claude): every character that is not an ASCII letter or digit becomes "-".
func dirName(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// readRule is the one Read rule of a run in cwd (newEnv uses a temporary
// folder without symlinks).
func readRule(cwd string) string {
	return "Read(~/.claude/projects/" + dirName(cwd) + "/**)"
}

// withRead is a copy of agents with Read added to each subagent's tools.
func withRead(agents map[string]any) map[string]any {
	copied := map[string]any{}
	for name, value := range agents {
		agent := map[string]any{}
		for key, field := range value.(map[string]any) {
			agent[key] = field
		}
		agent["tools"] = append(append([]any{}, agent["tools"].([]any)...), "Read")
		copied[name] = agent
	}
	return copied
}

func argAfter(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

// assertNoLeftovers checks that nothing of the run is left on disk: no
// leadin-run-* folder, no project folder, no task output folder.
func (e *env) assertNoLeftovers() {
	e.t.Helper()
	entries, _ := os.ReadDir(e.tmp)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "leadin-run-") {
			e.t.Errorf("%s left in TMPDIR", entry.Name())
		}
		if strings.HasPrefix(entry.Name(), "claude-") {
			tasks, _ := os.ReadDir(filepath.Join(e.tmp, entry.Name()))
			if len(tasks) != 0 {
				e.t.Errorf("task output folders left: %v", tasks)
			}
		}
	}
	projects, _ := os.ReadDir(filepath.Join(e.home, ".claude", "projects"))
	if len(projects) != 0 {
		e.t.Errorf("project folders left: %v", projects)
	}
}

func assertCode(t *testing.T, r result, code string) {
	t.Helper()
	if len(r.events) == 0 || r.last()["type"] != "run_failed" || r.last()["code"] != code {
		t.Fatalf("events = %v, want run_failed %s", r.events, code)
	}
	if r.fields["outcome"] != "error" || r.fields["error"] != code {
		t.Fatalf("fields = %v", r.fields)
	}
}

func keys(event map[string]any) []string {
	names := []string{}
	for name := range event {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the process tree and the folders are checked on Unix")
	}
}

func TestRedeemErrorsStopBeforeLookingForClaude(t *testing.T) {
	for _, tc := range []struct {
		status int
		raw    string
		code   string
	}{
		{http.StatusNotFound, `{"code": "invalid_ticket"}`, "ticket_rejected"},
		{http.StatusConflict, `{"code": "helper_outdated", "min_version": "9.0.0"}`, "helper_outdated"},
		{http.StatusConflict, `{"detail": "already finished"}`, "server_error"},
		{http.StatusInternalServerError, `{}`, "server_error"},
		{http.StatusFound, `{}`, "server_error"},
		{http.StatusOK, `not json`, "server_error"},
	} {
		e := newEnv(t)
		e.server.status, e.server.raw = tc.status, tc.raw
		r := e.run(0)
		assertCode(t, r, tc.code)
		if len(r.events) != 1 || !slices.Equal(keys(r.last()), []string{"code", "helper_version", "type"}) {
			t.Fatalf("%s: events = %v", tc.code, r.events)
		}
		if len(e.calls()) != 0 {
			t.Fatalf("%s: claude was called: %v", tc.code, e.calls())
		}
	}
}

func TestInvalidSpecNeverStartsClaude(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"unknown model": func(s map[string]any) { s["model"] = "gpt-5" },
		"another run":   func(s map[string]any) { s["run_id"] = 8 },
	} {
		e := newEnv(t)
		edit(e.server.spec)
		r := e.run(0)
		assertCode(t, r, "invalid_spec")
		if len(r.events) != 1 || len(e.calls()) != 0 {
			t.Fatalf("%s: events %v, calls %v", name, r.events, e.calls())
		}
	}
}

func TestUnreachableServerIsReported(t *testing.T) {
	e := newEnv(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	listener.Close()
	testutil.WriteFakeConfig(t, e.claude, e.config)
	request, _ := ParseStart(startMessage(url, nil))
	var out bytes.Buffer
	fields := Run(request, strings.NewReader(""), &out, logs.New(e.log))
	event, _ := protocol.ReadMessage(&out)
	if event["code"] != "server_unreachable" || fields["error"] != "server_unreachable" || len(e.calls()) != 0 {
		t.Fatalf("event %v, fields %v, calls %v", event, fields, e.calls())
	}
}

func TestClaudeNotFoundComesAfterRedeem(t *testing.T) {
	e := newEnv(t)
	t.Setenv("PATH", t.TempDir())
	r := e.run(0)
	assertCode(t, r, "claude_not_found")
	if len(e.server.requests) != 1 || !slices.Equal(keys(r.last()), []string{"code", "helper_version", "type"}) {
		t.Fatalf("requests %d, event %v", len(e.server.requests), r.last())
	}
}

func TestClaudeTooOldIsCheckedBeforeTheLogin(t *testing.T) {
	e := newEnv(t)
	e.config.Version = "2.1.247 (Claude Code)"
	r := e.run(0)
	assertCode(t, r, "claude_too_old")
	if r.last()["claude_version"] != "2.1.247" {
		t.Fatalf("event = %v", r.last())
	}
	calls := e.calls()
	if len(calls) != 1 || !slices.Equal(calls[0].Args, []string{"--version"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestClaudeNotLoggedInIsReported(t *testing.T) {
	e := newEnv(t)
	e.config.Auth = `{"loggedIn": false}`
	e.config.AuthExitCode = 1
	r := e.run(0)
	assertCode(t, r, "claude_not_logged_in")
	if r.last()["claude_version"] != "2.1.300" || r.last()["subscription_type"] != nil {
		t.Fatalf("event = %v", r.last())
	}
	if _, ok := r.last()["run_id"]; ok {
		t.Fatal("run_id in the event")
	}
	for _, call := range e.calls() {
		if call.Args[0] == "-p" {
			t.Fatal("claude -p ran")
		}
	}
}

func TestRunForwardsProgressAndFinalEvent(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	r := e.run(0)
	if len(e.server.requests) != 1 || e.server.paths[0] != "/api/v1/claude-runs/redeem/" {
		t.Fatalf("requests = %v %v", e.server.paths, e.server.requests)
	}
	if body := e.server.requests[0]; len(body) != 4 || body["run_id"] != 7.0 || body["ticket"] != testTicket ||
		body["helper_version"] != version.Version || body["protocol_version"] != float64(version.ProtocolVersion) {
		t.Fatalf("redeem body = %v", body)
	}
	types := r.types()
	if !slices.Equal(types, []string{"run_progress", "run_progress", "run_progress", "run_progress", "run_finished"}) {
		t.Fatalf("types = %v", types)
	}
	first := r.events[0]
	if first["key"] != "started" || first["count"] != json.Number("1") || len(first) != 3 {
		t.Fatalf("first event = %v", first)
	}
	finished := r.last()
	if finished["outcome"] != "done" || finished["message"] != "Stored." || finished["result_id"] != json.Number("42") {
		t.Fatalf("finished = %v", finished)
	}
	if finished["claude_version"] != "2.1.300" || finished["subscription_type"] != "pro" || finished["helper_version"] != "0.0.0-dev" {
		t.Fatalf("finished = %v", finished)
	}
	for _, key := range []string{"run_id", "item_id"} {
		if _, ok := finished[key]; ok {
			t.Fatalf("%s in run_finished", key)
		}
	}
	if r.fields["outcome"] != "done" || r.fields["result_id"] != json.Number("42") {
		t.Fatalf("fields = %v", r.fields)
	}
	e.assertNoLeftovers()
}

// The guarantees on the command (prompt and token out of the arguments,
// --restricted, --strict-mcp-config…) are in constraints_test.go.
func TestModelIDAndEffortComeFromSpec(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec["model"] = "claude-opus-5-5"
	e.server.spec["effort"] = "high"
	e.run(0)
	args := e.runCall().Args
	i := slices.Index(args, "--model")
	if !slices.Equal(args[i:i+4], []string{"--model", "claude-opus-5-5", "--effort", "high"}) {
		t.Fatalf("args = %q", args)
	}
}

func TestCommandComesFromSpec(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec["model"] = "opus"
	e.run(0)
	call := e.runCall()
	// The same command as the earlier helper, argument by argument.
	mcpConfig := argAfter(call.Args, "--mcp-config")
	want := []string{
		"-p", "--restricted", "--model", "opus", "--tools", "WebSearch,WebFetch,Read",
		"--allowedTools", "mcp__leadin WebSearch WebFetch " + readRule(call.Cwd), "--mcp-config", mcpConfig,
		"--strict-mcp-config", "--no-session-persistence", "--output-format", "stream-json",
		"--verbose", "--input-format", "stream-json",
	}
	if !slices.Equal(call.Args, want) || mcpConfig != filepath.Join(filepath.Dir(call.Cwd), "config", "mcp.json") {
		t.Fatalf("args = %q", call.Args)
	}
	// The spec's prompt arrives on stdin as one user message.
	var message map[string]any
	if err := json.Unmarshal([]byte(call.Stdin), &message); err != nil {
		t.Fatal(err)
	}
	if message["type"] != "user" || message["message"].(map[string]any)["role"] != "user" {
		t.Fatalf("stdin = %q", call.Stdin)
	}
	// The token is only in mcp.json, with the spec's URL.
	var config map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(call.MCPConfig), &config); err != nil {
		t.Fatal(err)
	}
	server := config["mcpServers"]["leadin"]
	if server["url"] != e.server.URL+"/mcp" || server["headers"].(map[string]any)["Authorization"] != "Bearer "+testToken {
		t.Fatalf("mcp.json = %s", call.MCPConfig)
	}
}

func TestInheritedConfigDirDoesNotMoveCleanup(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	other := filepath.Join(e.home, "other-config")
	t.Setenv("CLAUDE_CONFIG_DIR", other)
	e.config.Project = true
	e.run(0)
	if slices.Contains(e.runCall().Env, "CLAUDE_CONFIG_DIR") {
		t.Fatal("CLAUDE_CONFIG_DIR reached claude")
	}
	// claude used ~/.claude, and that is where the cleanup happens.
	e.assertNoLeftovers()
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("other config: %v", err)
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func assertDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for testutil.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if testutil.Alive(pid) {
		t.Fatalf("child %d still alive", pid)
	}
}

func TestEOFKillsClaudeProcessTree(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.DelayMS = 1000
	e.config.Child = filepath.Join(t.TempDir(), "child.pid")
	e.config.Project = true
	started := time.Now()
	r := e.run(1500 * time.Millisecond)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
	// Cancelled: no final event.
	if slices.Contains(r.types(), "run_finished") || slices.Contains(r.types(), "run_failed") {
		t.Fatalf("events = %v", r.events)
	}
	if len(r.fields) != 1 || r.fields["outcome"] != "cancelled" {
		t.Fatalf("fields = %v", r.fields)
	}
	assertDead(t, readPid(t, e.config.Child))
	e.assertNoLeftovers()
}

func TestCancelStopsSubagentsRun(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	e.config.DelayMS = 50
	e.config.Child = filepath.Join(t.TempDir(), "child.pid")
	started := time.Now()
	r := e.run(1500 * time.Millisecond)
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
	// Halfway: some progress, no final event.
	types := r.types()
	if !slices.Contains(types, "run_progress") || slices.Contains(types, "run_finished") || slices.Contains(types, "run_failed") {
		t.Fatalf("types = %v", types)
	}
	assertDead(t, readPid(t, e.config.Child))
	e.assertNoLeftovers()
	if r.fields["outcome"] != "cancelled" {
		t.Fatalf("fields = %v", r.fields)
	}
}

func TestTimeoutReportsTimeout(t *testing.T) {
	skipOnWindows(t)
	maxRunDuration = time.Second
	t.Cleanup(func() { maxRunDuration = MaxRunDuration })
	e := newEnv(t)
	e.config.DelayMS = 1000
	e.config.Child = filepath.Join(t.TempDir(), "child.pid")
	r := e.run(0)
	assertCode(t, r, "timeout")
	if r.last()["claude_version"] != "2.1.300" || r.last()["subscription_type"] != "pro" || slices.Contains(r.types(), "run_finished") {
		t.Fatalf("events = %v", r.events)
	}
	assertDead(t, readPid(t, e.config.Child))
	e.assertNoLeftovers()
}

func TestMCPUnauthorizedFromStream(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Fixture = fixture(t, "run_mcp_failed.jsonl")
	r := e.run(0)
	assertCode(t, r, "mcp_unauthorized")
	e.assertNoLeftovers()
}

func TestAPIErrorFromStreamCarriesMetrics(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Fixture = fixture(t, "run_api_error.jsonl")
	r := e.run(0)
	assertCode(t, r, "api_error")
	if r.last()["num_turns"] != json.Number("3") || r.last()["cost_usd"] != json.Number("0.0421") {
		t.Fatalf("event = %v", r.last())
	}
}

func TestProgressKeysComeFromProgressMap(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	r := e.run(0)
	got := []string{}
	for _, event := range r.progress() {
		got = append(got, event["key"].(string)+":"+event["count"].(json.Number).String())
	}
	if !slices.Equal(got, []string{"started:1", "searching_web:1", "item_saved:1", "item_saved:2"}) {
		t.Fatalf("progress = %v", got)
	}
	for _, line := range []string{"action=claude_start run_id=7", "action=progress run_id=7 key=started", "action=progress run_id=7 key=searching_web", "action=progress run_id=7 key=item_saved"} {
		if !strings.Contains(r.log, line+"\n") {
			t.Errorf("%q missing from the log %q", line, r.log)
		}
	}
}

func TestToolsMissingFromProgressMapAreWorking(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec["progress_map"] = map[string]any{"start_example": "started"}
	r := e.run(0)
	seen := map[string]bool{}
	for _, event := range r.progress() {
		seen[event["key"].(string)] = true
	}
	if len(seen) != 2 || !seen["started"] || !seen["working"] {
		t.Fatalf("keys = %v", seen)
	}
}

func TestSpecWithoutAgentsStartsWithoutSubagents(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.run(0)
	call := e.runCall()
	if argAfter(call.Args, "--tools") != "WebSearch,WebFetch,Read" || argAfter(call.Args, "--allowedTools") != "mcp__leadin WebSearch WebFetch "+readRule(call.Cwd) {
		t.Fatalf("args = %v", call.Args)
	}
	if slices.Contains(call.Args, "--agents") || len(call.Files) != 0 {
		t.Fatalf("args %v, files %v", call.Args, call.Files)
	}
}

func TestSpecWithAgentsStartsWithSubagents(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.server.spec = makeSpec(e.server.URL, true)
	e.config.Fixture = fixture(t, "run_subagents.jsonl")
	r := e.run(0)
	call := e.runCall()
	if argAfter(call.Args, "--tools") != "WebSearch,WebFetch,Agent,Read" || argAfter(call.Args, "--allowedTools") != "mcp__leadin WebSearch WebFetch Agent "+readRule(call.Cwd) {
		t.Fatalf("args = %v", call.Args)
	}
	if len(call.Files) != 0 {
		t.Fatalf("args %v, files %v", call.Args, call.Files)
	}
	if strings.Contains(strings.Join(call.Args, " "), testToken) {
		t.Fatal("token in the arguments")
	}
	// --agents holds the subagents of the spec, as JSON, with Read added:
	// they call the MCP tools too.
	var agents map[string]any
	if err := json.Unmarshal([]byte(call.Agents), &agents); err != nil {
		t.Fatal(err)
	}
	wantAgents, _ := json.Marshal(withRead(testAgents))
	gotAgents, _ := json.Marshal(agents)
	if string(gotAgents) != string(wantAgents) {
		t.Fatalf("--agents = %s, want %s", gotAgents, wantAgents)
	}

	last := map[string]string{}
	for _, event := range r.progress() {
		last[event["key"].(string)] = event["count"].(json.Number).String()
	}
	if last["group_done"] != "5" || last["group_started"] != "2" || last["working"] != "2" || last["summary_saved"] != "1" || last["completing"] != "1" {
		t.Fatalf("last counts = %v", last)
	}
	finished := r.last()
	if finished["type"] != "run_finished" || finished["outcome"] != "done" || finished["result_id"] != json.Number("42") || finished["cost_usd"] != json.Number("0.4127") {
		t.Fatalf("finished = %v", finished)
	}
	stats := finished["usage"].(map[string]any)["subagent_stats"].(map[string]any)
	if stats["spawned"] != json.Number("2") {
		t.Fatalf("subagent_stats = %v", stats)
	}
	if strings.Count(r.log, "key=group_done") != 5 || strings.Count(r.log, "key=working") != 2 {
		t.Fatalf("log = %q", r.log)
	}
	for _, leak := range []string{"Agent", "example-worker", "start_group", "mcp__", "TEXT"} {
		if strings.Contains(r.log, leak) {
			t.Errorf("%q in the log", leak)
		}
	}
	e.assertNoLeftovers()
}

func TestUsageLimitRunLogsCodeButNeverText(t *testing.T) {
	skipOnWindows(t)
	e := newEnv(t)
	e.config.Fixture = fixture(t, "run_usage_limit.jsonl")
	r := e.run(0)
	assertCode(t, r, "usage_limit")
	if r.last()["cost_usd"] != json.Number("0.1873") {
		t.Fatalf("event = %v", r.last())
	}
	if !strings.Contains(r.log, "key=item_saved") {
		t.Fatalf("log = %q", r.log)
	}
	// Claude's final text reaches the server as final_message, never the log
	// or the other events.
	if final, _ := r.last()["final_message"].(string); !strings.Contains(final, "hit your") {
		t.Fatalf("final_message = %q", r.last()["final_message"])
	}
	events, _ := json.Marshal(r.events[:len(r.events)-1])
	for _, text := range []string{r.log, string(events)} {
		if strings.Contains(text, "hit your") {
			t.Fatalf("the limit text leaked: %s", text)
		}
	}
}
