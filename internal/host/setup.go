package host

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/G-DPP/leadin-helper/internal/config"
	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/protocol"
	"github.com/G-DPP/leadin-helper/internal/setup"
	"github.com/G-DPP/leadin-helper/internal/version"
)

// setupEvents writes a step's events on the port, one at a time.
type setupEvents struct {
	mu        sync.Mutex
	w         io.Writer
	errorCode string
	finished  bool
}

func (e *setupEvents) write(message map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// stdout may be closed after a cancel: nobody to answer.
	_ = protocol.WriteMessage(e.w, message)
}

func (e *setupEvents) Started(step, command string) {
	e.write(map[string]any{"type": "setup_started", "step": step, "command": command})
}

func (e *setupEvents) Output(line string) {
	e.write(map[string]any{"type": "setup_output", "line": line})
}

func (e *setupEvents) Finished(step string, exitCode *int, errorCode string) {
	e.mu.Lock()
	e.finished = true
	e.errorCode = errorCode
	e.mu.Unlock()
	message := map[string]any{"type": "setup_finished", "step": step, "exit_code": nil, "error_code": errorCode}
	if exitCode != nil {
		message["exit_code"] = *exitCode
	}
	e.write(message)
}

// setupStep runs one setup step. The end of r (the port closed) cancels it.
func setupStep(request map[string]any, r io.Reader, w io.Writer, logger *logs.Logger) int {
	started := time.Now()
	events := &setupEvents{w: w}
	req, ok := setup.Parse(request)
	if !ok {
		step, _ := request["step"].(string)
		if !slices.Contains(setup.Steps, step) {
			step = ""
		}
		events.Finished(step, nil, setup.InvalidRequest)
		logger.Event(logs.Info, map[string]any{"action": "run_setup_step", "outcome": "error", "error": setup.InvalidRequest})
		return 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, r)
		cancel()
	}()
	setup.Run(ctx, req, events)
	fields := map[string]any{"action": "run_setup_step", "step": req.Step, "outcome": "ok",
		"duration_ms": time.Since(started).Milliseconds()}
	if events.errorCode != "" {
		fields["outcome"] = "error"
		fields["error"] = events.errorCode
	}
	logger.Event(logs.Info, fields)
	return 0
}

// diagnosticsLogLines is how much of the log Diagnostics copies.
const diagnosticsLogLines = 50

// Diagnostics is a block of text the person copies into a message when
// something does not work: versions, get_info, the account (never the key)
// and the end of the log. The log holds no secrets by design (internal/logs).
func Diagnostics() map[string]any {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", version.String())
	fmt.Fprintf(&b, "os: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	info := GetInfo()
	keys := make([]string, 0, len(info))
	for key := range info {
		if key != "type" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	b.WriteString("\nget_info:\n")
	for _, key := range keys {
		value, _ := json.Marshal(info[key])
		fmt.Fprintf(&b, "  %s: %s\n", key, value)
	}
	if path, err := config.ProfileDir(); err == nil && config.Load().Account == config.Profile {
		fmt.Fprintf(&b, "  profile_dir: %s\n", path)
	}
	b.WriteString("\nlog (last lines):\n")
	if path, err := logs.Path(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) > diagnosticsLogLines {
				lines = lines[len(lines)-diagnosticsLogLines:]
			}
			for _, line := range lines {
				b.WriteString("  " + line + "\n")
			}
		} else {
			b.WriteString("  (no log)\n")
		}
	}
	return map[string]any{"type": "diagnostics", "text": b.String()}
}
