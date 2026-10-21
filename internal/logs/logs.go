// Package logs is the helper's minimal local log.
//
// A line holds only the time, the level and a few allowed keys (action,
// outcome, identifiers): never prompts, Claude's text, tool contents, tickets,
// tokens or email addresses. Event drops every key outside AllowedFields.
// Nothing is ever written to stdout or stderr: if the file cannot be opened,
// the line is lost and the helper carries on.
package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// AllowedFields are the only keys that can reach the log, in this order.
// For tools only the neutral progress key (key), never the tool name.
var AllowedFields = []string{
	"action",
	"run_id",
	"result_id",
	"key",
	"duration_ms",
	"outcome",
	"error",
}

// Levels.
const (
	Info    = "INFO"
	Warning = "WARNING"
	Error   = "ERROR"
)

const (
	// MaxBytes is the size at which the log rotates.
	MaxBytes = 1 << 20
	// Backups is the number of rotated files kept (helper.log.1 … .3).
	Backups = 3
)

// DataDir is the user's LeadIn data folder, following each OS's conventions.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return dataDir(runtime.GOOS, home, os.Getenv), nil
}

func dataDir(goos, home string, getenv func(string) string) string {
	switch goos {
	case "windows":
		base := getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "LeadIn")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "LeadIn")
	}
	base := getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "leadin")
}

// Path is the log file: the same one the earlier helper wrote, since only
// one helper is registered at a time.
func Path() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs", "helper.log"), nil
}

// Logger appends lines to a rotating file. The zero value discards
// everything.
type Logger struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

// Open returns the logger for the standard log file, or one that discards
// everything if the data folder is unknown.
func Open() *Logger {
	path, err := Path()
	if err != nil {
		return &Logger{}
	}
	return New(path)
}

// New returns a logger writing to path.
func New(path string) *Logger {
	return &Logger{path: path, now: time.Now}
}

// Format is the key=value part of a line: only the allowed keys, in their
// order.
func Format(fields map[string]any) string {
	parts := make([]string, 0, len(AllowedFields))
	for _, key := range AllowedFields {
		if value, ok := fields[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	return strings.Join(parts, " ")
}

// Event writes one line with the allowed keys of fields. Other keys are
// dropped silently.
func (l *Logger) Event(level string, fields map[string]any) {
	if l == nil || l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stamp := l.now().UTC().Format(time.RFC3339)
	line := fmt.Sprintf("%s %s %s\n", stamp, level, Format(fields))
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return
	}
	l.rotateFor(len(line))
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}

// rotateFor rotates the log if writing size more bytes would reach MaxBytes,
// like Python's RotatingFileHandler: helper.log → .1 → .2 → .3, the oldest is
// dropped.
func (l *Logger) rotateFor(size int) {
	info, err := os.Stat(l.path)
	if err != nil || info.Size() == 0 || info.Size()+int64(size) < MaxBytes {
		return
	}
	for i := Backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	_ = os.Rename(l.path, l.path+".1")
}
