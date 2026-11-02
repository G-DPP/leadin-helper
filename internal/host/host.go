// Package host is the helper's main loop: it reads one request from Chrome
// and answers it.
//
// Two actions: get_info (one reply) and start_run (a sequence of events).
// For start_run the helper lives as long as the run: the extension opens one
// connectNative port per run and closes it to cancel the run.
package host

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/G-DPP/leadin-helper/internal/claude"
	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/protocol"
	"github.com/G-DPP/leadin-helper/internal/run"
	"github.com/G-DPP/leadin-helper/internal/version"
)

// actions are the actions answered with a single reply.
var actions = map[string]func() map[string]any{
	"get_info": GetInfo,
}

// GetInfo describes the helper and the claude it would run.
//
// It uses the run environment, so the dashboard sees the same login the runs
// will use.
func GetInfo() map[string]any {
	env := claude.RunEnvironment(os.Environ())
	path := claude.Find(env)
	info := map[string]any{
		"type":              "info",
		"helper_version":    version.Version,
		"protocol_version":  version.ProtocolVersion,
		"os":                runtime.GOOS,
		"arch":              runtime.GOARCH,
		"claude_found":      path != "",
		"claude_version":    nil,
		"logged_in":         false,
		"subscription_type": nil,
	}
	if path != "" {
		if v := claude.Version(path, env); v != "" {
			info["claude_version"] = v
		}
		loggedIn, subscription := claude.AuthStatus(path, env)
		info["logged_in"] = loggedIn
		if subscription != nil {
			info["subscription_type"] = *subscription
		}
	}
	return info
}

// Serve handles one request read from r, writes the reply to w and returns
// the exit code. Every failure becomes a reply: nothing is printed.
func Serve(r io.Reader, w io.Writer, logger *logs.Logger) (code int) {
	action := ""
	defer func() {
		if recovered := recover(); recovered != nil {
			// Only the kind: the panic value could contain data.
			logger.Event(logs.Error, map[string]any{"action": knownOr(action, "unknown"), "outcome": "error", "error": "panic"})
			fail(w, action)
			code = 1
		}
	}()

	request, err := protocol.ReadMessage(r)
	if errors.Is(err, io.EOF) {
		return 0
	}
	if errors.Is(err, protocol.ErrInvalid) {
		logger.Event(logs.Warning, map[string]any{"action": "invalid", "outcome": "error", "error": "invalid_request"})
		return reply(w, logger, "invalid", protocol.ErrorReply("invalid_request"))
	}
	if err != nil {
		logger.Event(logs.Error, map[string]any{"action": "unknown", "outcome": "error", "error": "read_failed"})
		fail(w, "")
		return 1
	}

	action, _ = request["action"].(string)
	if action == "start_run" {
		return startRun(request, r, w, logger)
	}
	handler, ok := actions[action]
	if !ok {
		logger.Event(logs.Info, map[string]any{"action": "invalid", "outcome": "error", "error": "invalid_request"})
		return reply(w, logger, "invalid", protocol.ErrorReply("invalid_request"))
	}
	if code := reply(w, logger, action, handler()); code != 0 {
		return code
	}
	logger.Event(logs.Info, map[string]any{"action": action, "outcome": "ok"})
	return 0
}

// startRun checks the request and executes the run. r is the rest of stdin:
// its end cancels the run.
func startRun(request map[string]any, r io.Reader, w io.Writer, logger *logs.Logger) int {
	started := time.Now()
	start, ok := run.ParseStart(request)
	if !ok {
		// No run_id in the log: it is not validated.
		code := reply(w, logger, "start_run", protocol.RunFailed("invalid_request", nil))
		logger.Event(logs.Info, map[string]any{"action": "start_run", "outcome": "error", "error": "invalid_request"})
		return code
	}
	fields := run.Run(start, r, w, logger)
	fields["action"] = "start_run"
	fields["run_id"] = start.RunID
	fields["duration_ms"] = time.Since(started).Milliseconds()
	logger.Event(logs.Info, fields)
	return 0
}

// reply writes message; if it cannot, it logs the failure and tries a
// generic error instead.
func reply(w io.Writer, logger *logs.Logger, action string, message map[string]any) int {
	err := protocol.WriteMessage(w, message)
	if err == nil {
		return 0
	}
	logger.Event(logs.Error, map[string]any{"action": action, "outcome": "error", "error": writeError(err)})
	if !errors.Is(err, protocol.ErrTooLarge) {
		// stdout is closed: there is nobody to answer.
		return 1
	}
	fail(w, action)
	return 1
}

// fail answers with the generic error of the action: run_failed for a run,
// error otherwise. A write error is ignored: stdout may be closed.
func fail(w io.Writer, action string) {
	if action == "start_run" {
		_ = protocol.WriteMessage(w, protocol.RunFailed("unknown", nil))
		return
	}
	_ = protocol.WriteMessage(w, protocol.ErrorReply("unknown"))
}

func knownOr(action, fallback string) string {
	if _, ok := actions[action]; ok || action == "start_run" {
		return action
	}
	return fallback
}

func writeError(err error) string {
	if errors.Is(err, protocol.ErrTooLarge) {
		return "too_large"
	}
	return fmt.Sprintf("%T", err)
}
