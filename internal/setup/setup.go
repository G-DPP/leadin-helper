// Package setup runs the installation steps the LeadIn setup page asks for:
// installing Claude Code, logging in and out, choosing the account of the
// runs, checking that Claude Code answers.
//
// The steps are a fixed list in the code: the website only picks a name and
// a few validated options, never a command. Each step reports the real
// command (for the page's Details), its output line by line and an exit
// code. The output is that of public commands (the Claude Code installer,
// `claude auth`): it can go to the page. Run output never does.
package setup

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/G-DPP/leadin-helper/internal/claude"
	"github.com/G-DPP/leadin-helper/internal/config"
	"github.com/G-DPP/leadin-helper/internal/proc"
)

// Step names.
const (
	InstallClaude = "install_claude"
	LoginClaude   = "login_claude"
	LogoutClaude  = "logout_claude"
	SetConfig     = "set_claude_config"
	CheckClaude   = "check_claude"
)

// Steps are the known steps, in the order of the setup page.
var Steps = []string{InstallClaude, LoginClaude, LogoutClaude, SetConfig, CheckClaude}

// Error codes of setup_finished.
const (
	InvalidRequest    = "invalid_request"
	NetworkError      = "network_error"
	InstallFailed     = "install_failed"
	ClaudeNotFound    = "claude_not_found"
	LoginFailed       = "login_failed"
	NotLoggedIn       = "claude_not_logged_in"
	InvalidAPIKey     = "invalid_api_key"
	UsageLimit        = "usage_limit"
	CheckFailed       = "check_failed"
	ConfigFailed      = "config_failed"
	Timeout           = "timeout"
	Cancelled         = "cancelled"
	maxLineCharacters = 2000
	maxLines          = 5000
)

// Timeouts per step: the installer downloads ~100 MB; a login waits for the
// person in the browser (shorter than the 600 s of the spike, Windows
// results: the page keeps Cancel visible).
var timeouts = map[string]time.Duration{
	InstallClaude: 10 * time.Minute,
	LoginClaude:   5 * time.Minute,
	LogoutClaude:  30 * time.Second,
	CheckClaude:   2 * time.Minute,
}

// CheckPrompt is the fixed prompt of check_claude.
const CheckPrompt = "Reply with OK."

// Request is a validated run_setup_step.
type Request struct {
	Step    string
	Options map[string]string
}

// Events receives what a step does. Started carries the command shown in
// Details; Output one line; Finished the end, once.
type Events interface {
	Started(step, command string)
	Output(line string)
	Finished(step string, exitCode *int, errorCode string)
}

// Parse checks a run_setup_step request: a known step and only the options
// it accepts, all strings.
func Parse(message map[string]any) (Request, bool) {
	step, _ := message["step"].(string)
	allowed := map[string][]string{
		InstallClaude: nil,
		LoginClaude:   {"method"},
		LogoutClaude:  nil,
		SetConfig:     {"account", "api_key", "claude_path"},
		CheckClaude:   nil,
	}
	names, known := allowed[step]
	if !known {
		return Request{}, false
	}
	options := map[string]string{}
	if raw, present := message["options"]; present && raw != nil {
		fields, ok := raw.(map[string]any)
		if !ok {
			return Request{}, false
		}
		for key, value := range fields {
			text, isText := value.(string)
			if !isText || !contains(names, key) {
				return Request{}, false
			}
			options[key] = text
		}
	}
	switch step {
	case LoginClaude:
		if method := options["method"]; method != "subscription" && method != "console" {
			return Request{}, false
		}
	case SetConfig:
		if !contains(config.Accounts, options["account"]) || !config.ValidClaudePath(options["claude_path"]) {
			return Request{}, false
		}
		if key, given := options["api_key"]; given && !config.ValidAPIKey(key) {
			return Request{}, false
		}
	}
	return Request{Step: step, Options: options}, true
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// Run executes req. Cancelling ctx (the port closed) stops the step and its
// whole process tree.
func Run(ctx context.Context, req Request, events Events) {
	switch req.Step {
	case InstallClaude:
		installClaude(ctx, events)
	case LoginClaude:
		loginClaude(ctx, req.Options["method"], events)
	case LogoutClaude:
		logoutClaude(ctx, events)
	case SetConfig:
		setConfig(req.Options, events)
	case CheckClaude:
		checkClaude(ctx, events)
	default:
		events.Finished(req.Step, nil, InvalidRequest)
	}
}

// installCommand is the official native installer of Claude Code: no
// administrator rights, into ~/.local/bin (F0 results: ~30 s, PATH not
// touched, so the helper looks there itself).
// installCommand is a variable so the tests use a script of their own.
var installCommand = officialInstaller

func officialInstaller() (shown string, args []string) {
	if runtime.GOOS == "windows" {
		script := "irm https://claude.ai/install.ps1 | iex"
		return "powershell -NoProfile -ExecutionPolicy Bypass -Command \"" + script + "\"",
			[]string{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script}
	}
	script := "curl -fsSL https://claude.ai/install.sh | bash"
	return script, []string{"bash", "-c", script}
}

// networkErrors are output fragments of a missing network, from curl and
// PowerShell.
var networkErrors = regexp.MustCompile(`(?i)could not resolve host|curl: \((6|7|28|35)\)|failed to connect|unable to connect|no such host is known|remote name could not be resolved|network is unreachable`)

func installClaude(ctx context.Context, events Events) {
	shown, args := installCommand()
	events.Started(InstallClaude, shown)
	// The person's own environment: the installer needs HOME, PATH and the
	// proxies. Nothing secret is passed.
	code, output, reason := execute(ctx, args, nil, os.Environ(), timeouts[InstallClaude], events)
	switch {
	case reason != "":
		events.Finished(InstallClaude, code, reason)
	case code != nil && *code == 0 && findClaude() != "":
		events.Finished(InstallClaude, code, "")
	case networkErrors.MatchString(output):
		events.Finished(InstallClaude, code, NetworkError)
	default:
		events.Finished(InstallClaude, code, InstallFailed)
	}
}

// loginEnvironment is the person's environment without any inherited
// ANTHROPIC_* or CLAUDE_* variable, plus the chosen account. Not the run's
// allowlist: opening the browser needs the desktop variables (DISPLAY,
// DBUS_SESSION_BUS_ADDRESS, XDG_*…), as in the F0 login spike.
func loginEnvironment(settings config.Config) ([]string, error) {
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "CLAUDE_") {
			continue
		}
		env = append(env, entry)
	}
	return settings.Apply(env)
}

func findClaude() string {
	settings := config.Load()
	env, err := settings.Apply(claude.RunEnvironment(os.Environ()))
	if err != nil {
		env = claude.RunEnvironment(os.Environ())
	}
	return claude.FindWith(env, settings.ClaudePath)
}

func loginClaude(ctx context.Context, method string, events Events) {
	settings := config.Load()
	path := claude.FindWith(claude.RunEnvironment(os.Environ()), settings.ClaudePath)
	flag := map[string]string{"subscription": "--claudeai", "console": "--console"}[method]
	args := []string{path, "auth", "login", flag}
	events.Started(LoginClaude, commandLine(append([]string{"claude"}, args[1:]...)))
	if path == "" {
		events.Finished(LoginClaude, nil, ClaudeNotFound)
		return
	}
	if settings.Account == config.APIKey {
		// With a key there is nothing to log in to.
		events.Finished(LoginClaude, nil, InvalidRequest)
		return
	}
	env, err := loginEnvironment(settings)
	if err != nil {
		events.Finished(LoginClaude, nil, LoginFailed)
		return
	}
	code, _, reason := execute(ctx, args, nil, env, timeouts[LoginClaude], events)
	if reason != "" {
		events.Finished(LoginClaude, code, reason)
		return
	}
	if loggedIn, _ := claude.AuthStatus(path, mustApply(settings)); loggedIn && code != nil && *code == 0 {
		events.Finished(LoginClaude, code, "")
		return
	}
	events.Finished(LoginClaude, code, LoginFailed)
}

func logoutClaude(ctx context.Context, events Events) {
	settings := config.Load()
	path := claude.FindWith(claude.RunEnvironment(os.Environ()), settings.ClaudePath)
	args := []string{path, "auth", "logout"}
	events.Started(LogoutClaude, "claude auth logout")
	if path == "" {
		events.Finished(LogoutClaude, nil, ClaudeNotFound)
		return
	}
	env, err := loginEnvironment(settings)
	if err != nil || settings.Account == config.APIKey {
		events.Finished(LogoutClaude, nil, InvalidRequest)
		return
	}
	code, _, reason := execute(ctx, args, nil, env, timeouts[LogoutClaude], events)
	if reason != "" {
		events.Finished(LogoutClaude, code, reason)
		return
	}
	events.Finished(LogoutClaude, code, "")
}

// mustApply is the run environment of settings, or the plain one if the
// account cannot be applied (no key saved).
func mustApply(settings config.Config) []string {
	env, err := settings.Apply(claude.RunEnvironment(os.Environ()))
	if err != nil {
		return claude.RunEnvironment(os.Environ())
	}
	return env
}

func setConfig(options map[string]string, events Events) {
	events.Started(SetConfig, "save the helper settings (config.json)")
	settings := config.Config{Account: options["account"], ClaudePath: options["claude_path"]}
	fail := func() { events.Finished(SetConfig, nil, ConfigFailed) }
	if key, given := options["api_key"]; given {
		if err := config.SetAPIKey(key); err != nil {
			fail()
			return
		}
		events.Output("API key saved (not shown).")
	}
	if settings.Account == config.APIKey && config.StoredAPIKey() == "" {
		events.Output("No API key saved: enter one.")
		events.Finished(SetConfig, nil, InvalidRequest)
		return
	}
	if settings.Account != config.APIKey {
		if err := config.RemoveAPIKey(); err != nil {
			fail()
			return
		}
	}
	if err := config.Save(settings); err != nil {
		fail()
		return
	}
	events.Output("Account for LeadIn runs: " + settings.Account)
	if settings.ClaudePath != "" {
		events.Output("Claude Code: " + settings.ClaudePath)
	}
	zero := 0
	events.Finished(SetConfig, &zero, "")
}

// checkErrors map an output fragment of a failed check to its code.
var checkErrors = []struct {
	pattern *regexp.Regexp
	code    string
}{
	{regexp.MustCompile(`(?i)invalid api key|invalid x-api-key|authentication_error`), InvalidAPIKey},
	{regexp.MustCompile(`(?i)not logged in|please run /login|/login`), NotLoggedIn},
	{regexp.MustCompile(`(?i)usage limit|rate limit|credit balance is too low|quota`), UsageLimit},
}

// checkClaude sends one minimal request, as a run would: the same account,
// --restricted, no tools, no MCP. With an API key logged_in is true even
// for a wrong key: only a real request tells.
func checkClaude(ctx context.Context, events Events) {
	settings := config.Load()
	env, err := settings.Apply(claude.RunEnvironment(os.Environ()))
	path := ""
	if err == nil {
		path = claude.FindWith(env, settings.ClaudePath)
	}
	args := []string{path, "-p", "--restricted", "--tools", "", "--strict-mcp-config", "--max-turns", "1",
		"--no-session-persistence", "--output-format", "text", CheckPrompt}
	events.Started(CheckClaude, commandLine(append([]string{"claude"}, args[1:]...)))
	if err != nil {
		events.Finished(CheckClaude, nil, NotLoggedIn)
		return
	}
	if path == "" {
		events.Finished(CheckClaude, nil, ClaudeNotFound)
		return
	}
	workdir, err := os.MkdirTemp("", "leadin-check-")
	if err != nil {
		events.Finished(CheckClaude, nil, CheckFailed)
		return
	}
	defer func() {
		claude.RemoveRunDirs(workdir, settings.ClaudeConfigDir())
		os.RemoveAll(workdir)
	}()
	code, output, reason := execute(ctx, args, &workdir, env, timeouts[CheckClaude], events)
	if reason != "" {
		events.Finished(CheckClaude, code, reason)
		return
	}
	if code != nil && *code == 0 {
		events.Finished(CheckClaude, code, "")
		return
	}
	for _, known := range checkErrors {
		if known.pattern.MatchString(output) {
			events.Finished(CheckClaude, code, known.code)
			return
		}
	}
	events.Finished(CheckClaude, code, CheckFailed)
}

// execute runs args with env, sends stdout and stderr line by line and
// returns the exit code (nil if the command did not start), all the output
// and a reason ("timeout", "cancelled", or "" when it ended by itself).
// stdin is closed: no step reads from it (the login must not wait for a key).
func execute(ctx context.Context, args []string, dir *string, env []string, timeout time.Duration, events Events) (*int, string, string) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	if dir != nil {
		cmd.Dir = *dir
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, "", ""
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	tree, err := proc.Start(cmd)
	writer.Close()
	if err != nil {
		reader.Close()
		events.Output(err.Error())
		return nil, err.Error(), ""
	}

	var output strings.Builder
	var mu sync.Mutex
	lines := make(chan struct{})
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		count := 0
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > maxLineCharacters {
				line = line[:maxLineCharacters]
			}
			// claude auth can print the account's address: it never leaves
			// the helper, as in get_info.
			line = email.ReplaceAllString(line, "<email>")
			mu.Lock()
			output.WriteString(line + "\n")
			mu.Unlock()
			if count++; count <= maxLines {
				events.Output(line)
			}
		}
	}()

	reason := ""
	select {
	case <-tree.Done():
	case <-time.After(timeout):
		reason = Timeout
	case <-ctx.Done():
		reason = Cancelled
	}
	if reason != "" {
		tree.Kill()
		<-tree.Done()
	}
	<-lines
	reader.Close()
	mu.Lock()
	text := output.String()
	mu.Unlock()
	if cmd.ProcessState == nil {
		return nil, text, reason
	}
	code := cmd.ProcessState.ExitCode()
	return &code, text, reason
}

var email = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// commandLine shows args as one line, quoting what has spaces.
func commandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"'") {
			arg = "\"" + strings.ReplaceAll(arg, "\"", "\\\"") + "\""
		}
		quoted[i] = arg
	}
	return strings.Join(quoted, " ")
}
