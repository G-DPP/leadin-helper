// Command fakeclaude stands in for claude in the tests. It answers
// `--version` and `auth status` with the values of fakeclaude.json, read from
// its own folder, and can record each call: its arguments and the names
// (never the values) of the environment variables it received.
//
// Called with -p it plays a run: it reads stdin to the end and prints a
// fixture line by line. It can also pause before each line, start a child
// that keeps stdout open, and create the project folder Claude Code would
// create in ~/.claude/projects/. The record of a run also holds stdin, the
// working directory, the files in it, mcp.json (the one of --mcp-config) with
// its permissions and folder, and the value of MAX_MCP_OUTPUT_TOKENS.
//
// The configuration comes from a file and not from environment variables:
// the helper passes claude only the allowed variables, and a FAKE_CLAUDE_*
// variable would need a wider allowlist in the tests.
package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is the content of fakeclaude.json.
type Config struct {
	// Version is printed by --version; empty means exit 1.
	Version string `json:"version"`
	// Auth is printed as is by `auth status`; empty means exit 1.
	Auth string `json:"auth"`
	// AuthExitCode is the exit code of `auth status` after printing Auth.
	AuthExitCode int `json:"auth_exit_code"`
	// Record, if set, is a file where each call appends one JSON line.
	Record string `json:"record"`
	// Fixture is the .jsonl file a run prints.
	Fixture string `json:"fixture"`
	// DelayMS is the pause before each line of the fixture.
	DelayMS int `json:"delay_ms"`
	// Child, if set, is a file where a run writes the pid of a sleeping
	// child that shares its stdout.
	Child string `json:"child"`
	// Project makes a run create its folder in ~/.claude/projects/ with a
	// file inside, as WebFetch does with large files, and its folder in
	// $TMPDIR/claude-<uid>/, as background subagents do.
	Project bool `json:"project"`
}

// Call is one recorded invocation.
type Call struct {
	Args      []string `json:"args"`
	Env       []string `json:"env"`
	Stdin     string   `json:"stdin,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Files     []string `json:"files,omitempty"`
	MCPMode   string   `json:"mcp_mode,omitempty"`
	MCPConfig string   `json:"mcp_config,omitempty"`
	MCPDir    string   `json:"mcp_dir,omitempty"`
	Agents    string   `json:"agents,omitempty"`
	// MaxMCPOutputTokens is the one value of the environment recorded.
	MaxMCPOutputTokens string `json:"max_mcp_output_tokens,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	config := readConfig()
	if len(args) > 0 && args[0] == "-p" {
		return playRun(config, args)
	}
	if config.Record != "" {
		record(config.Record, Call{Args: args})
	}
	switch strings.Join(args, " ") {
	case "--version":
		if config.Version == "" {
			return 1
		}
		os.Stdout.WriteString(config.Version + "\n")
		return 0
	case "auth status":
		if config.Auth == "" {
			return 1
		}
		os.Stdout.WriteString(config.Auth + "\n")
		return config.AuthExitCode
	}
	return 2
}

func playRun(config Config, args []string) int {
	stdin, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	if config.Record != "" {
		call := Call{Args: args, Stdin: string(stdin), Cwd: cwd}
		entries, _ := os.ReadDir(cwd)
		for _, entry := range entries {
			call.Files = append(call.Files, entry.Name())
		}
		mcp := filepath.Join(cwd, "mcp.json")
		for i, arg := range args {
			if arg == "--agents" && i+1 < len(args) {
				call.Agents = args[i+1]
			}
			if arg == "--mcp-config" && i+1 < len(args) {
				mcp = args[i+1]
			}
		}
		if info, err := os.Stat(mcp); err == nil {
			call.MCPMode = strconv.FormatUint(uint64(info.Mode().Perm()), 8)
			data, _ := os.ReadFile(mcp)
			call.MCPConfig = string(data)
			call.MCPDir = filepath.Dir(mcp)
		}
		call.MaxMCPOutputTokens = os.Getenv("MAX_MCP_OUTPUT_TOKENS")
		record(config.Record, call)
	}

	if config.Project {
		home, _ := os.UserHomeDir()
		results := filepath.Join(home, ".claude", "projects", dirName(cwd), "s", "tool-results")
		if err := os.MkdirAll(results, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(results, "page.pdf"), []byte("x"), 0o644)
		}
		if runtime.GOOS != "windows" {
			tasks := filepath.Join(os.TempDir(), "claude-"+strconv.Itoa(os.Getuid()), dirName(cwd), "tasks")
			_ = os.MkdirAll(tasks, 0o755)
		}
	}

	if config.Child != "" {
		child := exec.Command(sleepPath(), "60")
		child.Stdout = os.Stdout
		if err := child.Start(); err == nil {
			_ = os.WriteFile(config.Child, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		}
	}

	data, err := os.ReadFile(config.Fixture)
	if err != nil {
		return 3
	}
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" {
			continue
		}
		if config.DelayMS > 0 {
			time.Sleep(time.Duration(config.DelayMS) * time.Millisecond)
		}
		if _, err := os.Stdout.WriteString(line); err != nil {
			return 4
		}
	}
	return 0
}

// dirName is the folder name Claude Code derives from a path.
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

// sleepPath finds sleep without PATH, which the tests reduce to the fake
// claude's folder.
func sleepPath() string {
	for _, path := range []string{"/bin/sleep", "/usr/bin/sleep"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "sleep"
}

func readConfig() Config {
	var config Config
	exe, err := os.Executable()
	if err != nil {
		return config
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "fakeclaude.json"))
	if err != nil {
		return config
	}
	_ = json.Unmarshal(data, &config)
	return config
}

func record(path string, call Call) {
	names := []string{}
	for _, entry := range os.Environ() {
		if name, _, found := strings.Cut(entry, "="); found && name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	call.Env = names
	line, err := json.Marshal(call)
	if err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	file.Write(append(line, '\n'))
}
