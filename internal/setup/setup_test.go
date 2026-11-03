package setup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/G-DPP/leadin-helper/internal/config"
	"github.com/G-DPP/leadin-helper/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Run(m)) }

// recorder keeps the events of a step.
type recorder struct {
	mu       sync.Mutex
	command  string
	lines    []string
	exitCode *int
	code     string
	finished int
}

func (r *recorder) Started(_, command string) { r.mu.Lock(); r.command = command; r.mu.Unlock() }
func (r *recorder) Output(line string)        { r.mu.Lock(); r.lines = append(r.lines, line); r.mu.Unlock() }
func (r *recorder) Finished(_ string, exitCode *int, code string) {
	r.mu.Lock()
	r.exitCode, r.code = exitCode, code
	r.finished++
	r.mu.Unlock()
}

// isolate gives the step an empty home and a PATH with only dir (and the
// shell, for the install scripts).
func isolate(t *testing.T, dir string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-inherited-must-not-pass-0000")
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited/must/not/pass")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	return home
}

func fakeClaude(t *testing.T, fake testutil.FakeConfig) (path, record string) {
	t.Helper()
	path = testutil.BuildFakeClaude(t)
	record = filepath.Join(t.TempDir(), "calls.jsonl")
	fake.Record = record
	fake.Version = "2.1.300 (Claude Code)"
	testutil.WriteFakeConfig(t, path, fake)
	return path, record
}

func run(t *testing.T, ctx context.Context, step string, options map[string]any) *recorder {
	t.Helper()
	message := map[string]any{"step": step}
	if options != nil {
		message["options"] = options
	}
	req, ok := Parse(message)
	if !ok {
		t.Fatalf("Parse(%v) failed", message)
	}
	events := &recorder{}
	Run(ctx, req, events)
	if events.finished != 1 {
		t.Fatalf("finished %d times", events.finished)
	}
	return events
}

func TestParseRejectsWhatIsNotInTheList(t *testing.T) {
	cases := []map[string]any{
		{"step": "rm_rf"},
		{"step": ""},
		{},
		{"step": LoginClaude},
		{"step": LoginClaude, "options": map[string]any{"method": "other"}},
		{"step": LoginClaude, "options": map[string]any{"method": "subscription", "extra": "x"}},
		{"step": InstallClaude, "options": map[string]any{"url": "https://evil"}},
		{"step": SetConfig, "options": map[string]any{"account": "root"}},
		{"step": SetConfig, "options": map[string]any{"account": "api_key", "api_key": "short"}},
		{"step": SetConfig, "options": map[string]any{"account": "current", "claude_path": "relative/claude"}},
		{"step": SetConfig, "options": map[string]any{"account": 3}},
		{"step": CheckClaude, "options": "x"},
	}
	for _, message := range cases {
		if _, ok := Parse(message); ok {
			t.Errorf("Parse(%v) accepted", message)
		}
	}
	for _, message := range []map[string]any{
		{"step": InstallClaude},
		{"step": LoginClaude, "options": map[string]any{"method": "console"}},
		{"step": SetConfig, "options": map[string]any{"account": "profile"}},
		{"step": CheckClaude, "options": nil},
	} {
		if _, ok := Parse(message); !ok {
			t.Errorf("Parse(%v) rejected", message)
		}
	}
}

func TestSetConfigSavesAccountAndKey(t *testing.T) {
	isolate(t, t.TempDir())
	events := run(t, context.Background(), SetConfig, map[string]any{"account": "api_key", "api_key": "sk-ant-api03-abcdefghijklmnop"})
	if events.code != "" || config.Load().Account != config.APIKey || config.StoredAPIKey() != "sk-ant-api03-abcdefghijklmnop" {
		t.Fatalf("code %q, config %+v", events.code, config.Load())
	}
	for _, line := range events.lines {
		if strings.Contains(line, "abcdefghijklmnop") {
			t.Fatalf("the key is in the output: %q", line)
		}
	}
	if runtime.GOOS != "windows" {
		dir, _ := filepath.Split(mustKeyPath(t))
		info, _ := os.Stat(filepath.Join(dir, "api_key"))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("api_key mode %o", info.Mode().Perm())
		}
	}
	// Back to the usual login: the key is deleted.
	run(t, context.Background(), SetConfig, map[string]any{"account": "current"})
	if config.Load().Account != config.Current || config.StoredAPIKey() != "" {
		t.Fatalf("config %+v, key still there", config.Load())
	}
}

func mustKeyPath(t *testing.T) string {
	t.Helper()
	dir, err := config.ProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSetConfigAPIKeyAccountNeedsAKey(t *testing.T) {
	isolate(t, t.TempDir())
	events := run(t, context.Background(), SetConfig, map[string]any{"account": "api_key"})
	if events.code != InvalidRequest || config.Load().Account != config.Current {
		t.Fatalf("code %q, config %+v", events.code, config.Load())
	}
}

func TestLoginRunsClaudeAndWaitsForTheLogin(t *testing.T) {
	path, record := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": false}`, LoginAuth: `{"loggedIn": true}`})
	isolate(t, filepath.Dir(path))
	events := run(t, context.Background(), LoginClaude, map[string]any{"method": "subscription"})
	if events.code != "" || events.exitCode == nil || *events.exitCode != 0 {
		t.Fatalf("code %q exit %v lines %q", events.code, events.exitCode, events.lines)
	}
	if events.command != "claude auth login --claudeai" {
		t.Fatalf("command %q", events.command)
	}
	if len(events.lines) == 0 || strings.Contains(events.lines[0], "@") || !strings.Contains(events.lines[0], "<email>") {
		t.Fatalf("lines %q", events.lines)
	}
	calls := testutil.ReadCalls(t, record)
	login := calls[0]
	if !slices.Equal(login.Args, []string{"auth", "login", "--claudeai"}) {
		t.Fatalf("args %q", login.Args)
	}
	if slices.Contains(login.Env, "ANTHROPIC_API_KEY") || slices.Contains(login.Env, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("inherited variables passed: %v", login.Env)
	}
}

func TestLoginWithProfileUsesItsFolder(t *testing.T) {
	path, record := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": false}`, LoginAuth: `{"loggedIn": true}`})
	isolate(t, filepath.Dir(path))
	if err := config.Save(config.Config{Account: config.Profile}); err != nil {
		t.Fatal(err)
	}
	run(t, context.Background(), LoginClaude, map[string]any{"method": "console"})
	login := testutil.ReadCalls(t, record)[0]
	if !slices.Contains(login.Env, "CLAUDE_CONFIG_DIR") || !slices.Equal(login.Args, []string{"auth", "login", "--console"}) {
		t.Fatalf("call %+v", login)
	}
	dir, _ := config.ProfileDir()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("profile folder: %v", err)
	}
}

func TestFailedLoginIsLoginFailed(t *testing.T) {
	path, _ := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": false}`, LoginExitCode: 1})
	isolate(t, filepath.Dir(path))
	if events := run(t, context.Background(), LoginClaude, map[string]any{"method": "subscription"}); events.code != LoginFailed {
		t.Fatalf("code %q", events.code)
	}
}

func TestLoginCanBeCancelled(t *testing.T) {
	path, _ := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": false}`, LoginDelayMS: 30_000})
	isolate(t, filepath.Dir(path))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	started := time.Now()
	events := run(t, ctx, LoginClaude, map[string]any{"method": "subscription"})
	if events.code != Cancelled || time.Since(started) > 10*time.Second {
		t.Fatalf("code %q after %s", events.code, time.Since(started))
	}
}

func TestLoginWithoutClaudeIsNotFound(t *testing.T) {
	isolate(t, t.TempDir())
	if events := run(t, context.Background(), LoginClaude, map[string]any{"method": "subscription"}); events.code != ClaudeNotFound {
		t.Fatalf("code %q", events.code)
	}
}

func TestLogoutLogsOut(t *testing.T) {
	path, record := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": true}`})
	isolate(t, filepath.Dir(path))
	if events := run(t, context.Background(), LogoutClaude, nil); events.code != "" {
		t.Fatalf("code %q", events.code)
	}
	if args := testutil.ReadCalls(t, record)[0].Args; !slices.Equal(args, []string{"auth", "logout"}) {
		t.Fatalf("args %q", args)
	}
}

func TestCheckSendsOneMinimalRequest(t *testing.T) {
	path, record := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": true}`, Check: "OK"})
	isolate(t, filepath.Dir(path))
	events := run(t, context.Background(), CheckClaude, nil)
	if events.code != "" {
		t.Fatalf("code %q lines %q", events.code, events.lines)
	}
	args := testutil.ReadCalls(t, record)[0].Args
	for _, want := range []string{"-p", "--restricted", "--strict-mcp-config", "--no-session-persistence", CheckPrompt} {
		if !slices.Contains(args, want) {
			t.Errorf("%q missing from %q", want, args)
		}
	}
	if i := slices.Index(args, "--tools"); i < 0 || args[i+1] != "" {
		t.Errorf("--tools must be empty: %q", args)
	}
}

func TestCheckRecognisesKnownErrors(t *testing.T) {
	for output, code := range map[string]string{
		"Invalid API key · Fix external API key": InvalidAPIKey,
		"Not logged in · Please run /login":      NotLoggedIn,
		"Claude AI usage limit reached":          UsageLimit,
		"something else":                         CheckFailed,
	} {
		path, _ := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": true}`, Check: output, CheckExitCode: 1})
		isolate(t, filepath.Dir(path))
		if events := run(t, context.Background(), CheckClaude, nil); events.code != code {
			t.Errorf("%q: code %q, want %q", output, events.code, code)
		}
	}
}

func TestCheckWithAPIKeyAccountPassesTheKey(t *testing.T) {
	path, record := fakeClaude(t, testutil.FakeConfig{Auth: `{"loggedIn": true}`, Check: "OK"})
	isolate(t, filepath.Dir(path))
	run(t, context.Background(), SetConfig, map[string]any{"account": "api_key", "api_key": "sk-ant-api03-abcdefghijklmnop"})
	run(t, context.Background(), CheckClaude, nil)
	if env := testutil.ReadCalls(t, record)[0].Env; !slices.Contains(env, "ANTHROPIC_API_KEY") || slices.Contains(env, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("env %v", env)
	}
}

func TestInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test installers are sh scripts")
	}
	saved := installCommand
	t.Cleanup(func() { installCommand = saved })
	for _, c := range []struct {
		script string
		claude bool
		code   string
	}{
		{"echo Downloading; echo Done", true, ""},
		{"echo 'curl: (6) Could not resolve host: claude.ai'; exit 6", false, NetworkError},
		{"echo broken; exit 1", false, InstallFailed},
		{"echo Done", false, InstallFailed},
	} {
		dir := t.TempDir()
		if c.claude {
			path, _ := fakeClaude(t, testutil.FakeConfig{})
			dir = filepath.Dir(path)
		}
		isolate(t, dir)
		script := c.script
		installCommand = func() (string, []string) { return "test installer", []string{"sh", "-c", script} }
		events := run(t, context.Background(), InstallClaude, nil)
		if events.code != c.code || events.command != "test installer" || len(events.lines) == 0 {
			t.Errorf("%q: code %q lines %q", c.script, events.code, events.lines)
		}
	}
}

func TestOfficialInstallerCommand(t *testing.T) {
	shown, args := officialInstaller()
	if runtime.GOOS == "windows" {
		if !strings.Contains(shown, "irm https://claude.ai/install.ps1 | iex") || args[0] != "powershell" {
			t.Fatalf("%q %q", shown, args)
		}
		return
	}
	if shown != "curl -fsSL https://claude.ai/install.sh | bash" || !slices.Equal(args, []string{"bash", "-c", shown}) {
		t.Fatalf("%q %q", shown, args)
	}
}
