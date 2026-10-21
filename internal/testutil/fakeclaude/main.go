// Command fakeclaude stands in for claude in the tests. It answers
// `--version` and `auth status` with the values of fakeclaude.json, read from
// its own folder, and can record its arguments and the names (never the
// values) of the environment variables it received.
//
// The configuration comes from a file and not from environment variables:
// the helper passes claude only the allowed variables, and a FAKE_CLAUDE_*
// variable would need a wider allowlist in the tests.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	config := readConfig()
	if config.Record != "" {
		record(config.Record, args)
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

func record(path string, args []string) {
	names := []string{}
	for _, entry := range os.Environ() {
		if name, _, found := strings.Cut(entry, "="); found && name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	line, err := json.Marshal(map[string]any{"args": args, "env": names})
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
