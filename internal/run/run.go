// Package run executes start_run: it redeems the ticket and runs
// `claude -p` with the run spec.
//
// One run: redeem the ticket on the server, which gives the run spec
// (package spec); check claude (present, recent enough and logged in); a
// temporary folder with work/ (claude's working directory) and config/ (with
// mcp.json); `claude -p` (with --agents if the spec has subagents) with the spec's prompt on stdin; read the stream
// (package stream) and close. The helper lives as long as the run: EOF on
// its stdin means the extension closed the port (cancel, or the browser
// closed), and then claude is stopped with all its children.
//
// The helper does not know which kinds of runs exist or which tools they
// use: everything comes from the server.
//
// The events written here carry no run_id: the extension adds it.
package run

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/G-DPP/leadin-helper/internal/claude"
	"github.com/G-DPP/leadin-helper/internal/config"
	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/proc"
	"github.com/G-DPP/leadin-helper/internal/protocol"
	"github.com/G-DPP/leadin-helper/internal/spec"
	"github.com/G-DPP/leadin-helper/internal/stream"
)

// MaxRunDuration caps a run: beyond it claude is stopped and the run fails
// with timeout. The spec cannot change it.
const MaxRunDuration = 3 * time.Hour

// maxRunDuration is MaxRunDuration; only the tests shorten it.
var maxRunDuration = MaxRunDuration

// afterStart runs once claude has started and is getting the prompt; only the
// tests set it, to check the cleanup after a panic.
var afterStart = func() {}

// Run executes the run of req, writing the events to out. stdin is the rest
// of the helper's stdin: its EOF cancels the run. It returns the fields for
// the log (outcome, error, result_id).
func Run(req StartRequest, stdin io.Reader, out io.Writer, logger *logs.Logger) map[string]any {
	// The ticket first: it is valid for a few seconds, and the checks on
	// claude can take several.
	data, err := spec.Redeem(context.Background(), req.ServerURL, req.RunID, req.Ticket)
	if err != nil {
		code := "server_error"
		var redeemErr *spec.RedeemError
		if errors.As(err, &redeemErr) {
			code = redeemErr.Code
		}
		return fail(out, code, nil)
	}
	runSpec, ok := spec.Validate(data, req.ServerURL, req.RunID)
	if !ok {
		return fail(out, "invalid_spec", nil)
	}

	// The same environment for the checks and for the run: the login checked
	// is the one claude will use.
	// The account chosen in the setup page (config): the profile's folder
	// or the API key are added to the filtered environment.
	settings := config.Load()
	env, err := settings.Apply(claude.RunEnvironment(os.Environ()))
	if err != nil {
		return fail(out, "claude_not_logged_in", nil)
	}
	path := claude.FindWith(env, settings.ClaudePath)
	if path == "" {
		return fail(out, "claude_not_found", nil)
	}
	var v versions
	if found := claude.Version(path, env); found != "" {
		v.claude = &found
	}
	if v.claude != nil && claude.IsTooOld(*v.claude) {
		return fail(out, "claude_too_old", v.fields(false))
	}
	loggedIn, subscription := claude.AuthStatus(path, env)
	v.subscription = subscription
	if !loggedIn {
		return fail(out, "claude_not_logged_in", v.fields(true))
	}

	logger.Event(logs.Info, map[string]any{"action": "claude_start", "run_id": req.RunID})
	return execute(req, runSpec, path, env, settings.ClaudeConfigDir(), v, stdin, out, logger)
}

// versions are claude's version and subscription type, nil when unknown.
type versions struct {
	claude, subscription *string
}

// fields are the versions for run_failed; the subscription type only once
// `claude auth status` has run.
func (v versions) fields(withSubscription bool) map[string]any {
	fields := map[string]any{"claude_version": nil}
	if v.claude != nil {
		fields["claude_version"] = *v.claude
	}
	if withSubscription {
		fields["subscription_type"] = nil
		if v.subscription != nil {
			fields["subscription_type"] = *v.subscription
		}
	}
	return fields
}

func execute(req StartRequest, runSpec spec.RunSpec, path string, env []string, configDir string, v versions, stdin io.Reader, out io.Writer, logger *logs.Logger) map[string]any {
	// The run's folder: work/ is claude's working directory, config/ keeps
	// mcp.json out of Read's reach (--restricted lets Read into the working
	// directory, and Claude must not read the token).
	runDir, err := os.MkdirTemp("", "leadin-run-")
	if err != nil {
		return fail(out, "unknown", v.fields(true))
	}
	workdir := filepath.Join(runDir, "work")
	privateDir := filepath.Join(runDir, "config")
	var tree *proc.Tree
	// Cleanup in every case, panics included: stop claude if it is still
	// running, then remove the run's folders and the one with mcp.json (and
	// the token).
	defer func() {
		if tree != nil && !tree.Exited() {
			tree.Kill()
		}
		claude.RemoveRunDirs(workdir, configDir)
		_ = os.RemoveAll(runDir)
	}()
	if os.Mkdir(workdir, 0o700) != nil || os.Mkdir(privateDir, 0o700) != nil {
		return fail(out, "unknown", v.fields(true))
	}

	cmd, stdout, err := command(runSpec, path, env, workdir, privateDir, configDir)
	if err != nil {
		return fail(out, "unknown", v.fields(true))
	}
	defer stdout.Close()
	stdinPipe, err := cmd.StdinPipe()
	if err == nil {
		tree, err = proc.Start(cmd)
	}
	// Only claude (and its children) keep the write end of stdout open, so
	// the stream ends when they all exit.
	cmd.Stdout.(*os.File).Close()
	if err != nil {
		return fail(out, "unknown", v.fields(true))
	}

	// The supervisor starts before the prompt is written: if claude does not
	// read stdin, the write blocks, and it must still be possible to cancel.
	stop := make(chan struct{})
	reason, supervised := supervise(tree, cancelled(stdin), stop)
	go func() {
		// Once claude has exited (or has been stopped) the write fails: the
		// stream says what happened.
		_, _ = stdinPipe.Write(userMessage(runSpec.Prompt))
		_ = stdinPipe.Close()
	}()
	afterStart()

	reader := stream.NewReader(runSpec.ProgressMap, runSpec.Outcomes, runSpec.ResultIDKey, runSpec.FailureMessage)
	lines := bufio.NewReader(stdout)
	for {
		// ReadBytes and not a Scanner, which stops at 64 KB: a stream line
		// can be several MB.
		line, readErr := lines.ReadBytes('\n')
		if len(line) > 0 {
			events := reader.Feed(line)
			for _, key := range reader.LastProgress {
				logger.Event(logs.Info, map[string]any{"action": "progress", "run_id": req.RunID, "key": key})
			}
			// Once cancelled or timed out, no more progress.
			if reason.get() == "" {
				for _, event := range events {
					_ = protocol.WriteMessage(out, event)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	<-tree.Done()
	close(stop)
	<-supervised

	switch reason.get() {
	case "cancelled":
		// Whoever closed the port needs no answer.
		return map[string]any{"outcome": "cancelled"}
	case "timeout":
		return fail(out, "timeout", v.fields(true))
	}

	event := reader.FinalEvent(v.claude, v.subscription)
	_ = protocol.WriteMessage(out, event)
	if event["type"] == "run_failed" {
		return map[string]any{"outcome": "error", "error": event["code"]}
	}
	fields := map[string]any{"outcome": event["outcome"]}
	if event["result_id"] != nil {
		fields["result_id"] = event["result_id"]
	}
	return fields
}

// command prepares claude in workdir: mcp.json in privateDir, the command
// line (with the subagents if the spec has them, and Read only in the run's
// project folders), the filtered environment with the MCP answer limit,
// stderr discarded (it contains Claude's text) and stdout on a pipe whose
// read end is returned.
func command(runSpec spec.RunSpec, path string, env []string, workdir, privateDir, configDir string) (*exec.Cmd, *os.File, error) {
	mcpConfig, err := claude.WriteMCPConfig(privateDir, runSpec.MCPURL, runSpec.MCPToken)
	if err != nil {
		return nil, nil, err
	}
	agents := ""
	if len(runSpec.Agents) > 0 {
		if agents, err = claude.AgentsJSON(agentsWithRead(runSpec.Agents)); err != nil {
			return nil, nil, err
		}
	}
	args := claude.BuildCommand(path, runSpec.Model, runSpec.Effort, runSpec.Tools, mcpConfig, agents, claude.ReadRulesIn(workdir, configDir))
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = workdir
	cmd.Env = claude.RunEnv(env)
	// An *os.File and not cmd.StdoutPipe: the process can then be waited for
	// while the stream is still being read.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = stdoutWrite
	cmd.Stderr = nil
	return cmd, stdoutRead, nil
}

// agentsWithRead is a copy of agents with Read among each subagent's tools: a
// subagent calls the MCP tools too, and their answer can end up in a file.
// The Read rules hold for the whole session, subagents included.
func agentsWithRead(agents map[string]spec.Agent) map[string]spec.Agent {
	copied := make(map[string]spec.Agent, len(agents))
	for name, agent := range agents {
		if !slices.Contains(agent.Tools, claude.ReadTool) {
			agent.Tools = append(append([]string{}, agent.Tools...), claude.ReadTool)
		}
		copied[name] = agent
	}
	return copied
}

// cancelled is closed when stdin ends: the extension closed the port. After
// start_run the extension sends nothing else, so what arrives is discarded.
func cancelled(stdin io.Reader) <-chan struct{} {
	closed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		close(closed)
	}()
	return closed
}

// stopReason is why the supervisor stopped claude: "", cancelled or timeout.
type stopReason struct {
	mu    sync.Mutex
	value string
}

func (r *stopReason) set(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.value = value
}

func (r *stopReason) get() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value
}

// supervise stops claude when the run is cancelled or lasts more than
// maxRunDuration, until stop is closed. Stopping claude closes its stdout,
// so reading the stream ends by itself. Even after claude has exited, a
// child left alive can keep stdout open, and then the group is stopped.
func supervise(tree *proc.Tree, cancelled <-chan struct{}, stop <-chan struct{}) (*stopReason, <-chan struct{}) {
	reason := &stopReason{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(maxRunDuration)
		defer timer.Stop()
		select {
		case <-cancelled:
			reason.set("cancelled")
		case <-timer.C:
			reason.set("timeout")
		case <-stop:
			return
		}
		tree.Kill()
	}()
	return reason, done
}

// userMessage is the stdin line for `claude --input-format stream-json`,
// with the spec's prompt.
func userMessage(prompt string) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	// Encode ends the line with "\n".
	_ = encoder.Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
	return buf.Bytes()
}

func fail(out io.Writer, code string, fields map[string]any) map[string]any {
	_ = protocol.WriteMessage(out, protocol.RunFailed(code, fields))
	return map[string]any{"outcome": "error", "error": code}
}
