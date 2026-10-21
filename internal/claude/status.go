package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// VersionTimeout bounds `claude --version`.
	VersionTimeout = 10 * time.Second
	// AuthTimeout bounds `claude auth status`.
	AuthTimeout = 15 * time.Second
	// waitDelay bounds the wait for pipes left open by children of a killed
	// command.
	waitDelay = time.Second
)

// MinVersion is the first release with --restricted: «Added `--restricted`
// (or `CLAUDE_CODE_RESTRICTED=1`)» in the 2.1.248 section of Claude Code's
// CHANGELOG.md (github.com/anthropics/claude-code). An older claude would
// reject the flag as unknown.
var MinVersion = [3]int{2, 1, 248}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// IsTooOld is true only when version is recognized and older than
// MinVersion. An unrecognized format goes ahead: better an error from claude
// than a block on a new format.
func IsTooOld(version string) bool {
	match := versionPattern.FindStringSubmatch(version)
	if match == nil {
		return false
	}
	for i := range MinVersion {
		part, err := strconv.Atoi(match[i+1])
		if err != nil {
			return false
		}
		if part != MinVersion[i] {
			return part < MinVersion[i]
		}
	}
	return false
}

// runShort runs a short command with stdin closed, stderr discarded and a
// timeout. It returns stdout and whether the command could run to the end
// (a non-zero exit still counts as finished).
func runShort(path string, env []string, timeout time.Duration, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.WaitDelay = waitDelay
	hideWindow(cmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, false, err
	}
	return stdout.Bytes(), true, err
}

// Version is claude's version (e.g. "2.1.300"), or "" if it does not answer.
func Version(path string, env []string) string {
	out, _, err := runShort(path, env, VersionTimeout, "--version")
	if err != nil {
		return ""
	}
	// "2.1.300 (Claude Code)" → "2.1.300"
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// AuthStatus is `claude auth status` reduced to loggedIn and
// subscriptionType. Any error (process, timeout, invalid JSON) counts as not
// logged in. With the run environment the check sees the run's account.
func AuthStatus(path string, env []string) (loggedIn bool, subscriptionType *string) {
	out, finished, _ := runShort(path, env, AuthTimeout, "auth", "status")
	if !finished {
		return false, nil
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil || data == nil {
		return false, nil
	}
	loggedIn, _ = data["loggedIn"].(bool)
	if value, ok := data["subscriptionType"].(string); ok {
		subscriptionType = &value
	}
	return loggedIn, subscriptionType
}
