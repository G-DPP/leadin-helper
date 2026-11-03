// Package testutil holds helpers shared by the tests.
package testutil

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// FakeConfig is the content of fakeclaude.json (see the fakeclaude command).
type FakeConfig struct {
	Version       string `json:"version,omitempty"`
	Auth          string `json:"auth,omitempty"`
	AuthExitCode  int    `json:"auth_exit_code,omitempty"`
	Record        string `json:"record,omitempty"`
	Fixture       string `json:"fixture,omitempty"`
	DelayMS       int    `json:"delay_ms,omitempty"`
	Child         string `json:"child,omitempty"`
	Project       bool   `json:"project,omitempty"`
	LoginAuth     string `json:"login_auth,omitempty"`
	LoginExitCode int    `json:"login_exit_code,omitempty"`
	LoginDelayMS  int    `json:"login_delay_ms,omitempty"`
	Check         string `json:"check,omitempty"`
	CheckExitCode int    `json:"check_exit_code,omitempty"`
}

// Call is one recorded invocation of the fake claude. Stdin, Cwd, Files,
// MCPMode (octal), MCPConfig and Agents are recorded for runs (-p) only.
type Call struct {
	Args      []string `json:"args"`
	Env       []string `json:"env"`
	Stdin     string   `json:"stdin"`
	Cwd       string   `json:"cwd"`
	Files     []string `json:"files"`
	MCPMode   string   `json:"mcp_mode"`
	MCPConfig string   `json:"mcp_config"`
	MCPDir    string   `json:"mcp_dir"`
	Agents    string   `json:"agents"`
	// MaxMCPOutputTokens is the one value of the environment recorded.
	MaxMCPOutputTokens string `json:"max_mcp_output_tokens"`
}

var (
	buildOnce sync.Once
	buildDir  string
	buildPath string
	buildErr  error
)

// Run runs the tests of a package and removes the fake claude built for
// them. Use it from TestMain: os.Exit(testutil.Run(m)).
func Run(m *testing.M) int {
	code := m.Run()
	if buildDir != "" {
		os.RemoveAll(buildDir)
	}
	return code
}

// ClaudeName is the executable name of claude on this OS.
func ClaudeName() string {
	if runtime.GOOS == "windows" {
		return "claude.exe"
	}
	return "claude"
}

// BuildFakeClaude compiles the fake claude once per package and copies it
// into a new folder of the test, named claude (claude.exe on Windows). It
// returns the path of the copy; its configuration goes next to it with
// WriteFakeConfig.
func BuildFakeClaude(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "leadin-fakeclaude-")
		if buildErr != nil {
			return
		}
		buildPath = filepath.Join(buildDir, ClaudeName())
		cmd := exec.Command(goTool(), "build", "-o", buildPath, "github.com/G-DPP/leadin-helper/internal/testutil/fakeclaude")
		cmd.Stderr = os.Stderr
		buildErr = cmd.Run()
	})
	if buildErr != nil {
		t.Fatalf("building the fake claude: %v", buildErr)
	}
	target := filepath.Join(t.TempDir(), ClaudeName())
	if err := copyFile(buildPath, target); err != nil {
		t.Fatal(err)
	}
	return target
}

// WriteFakeConfig writes fakeclaude.json next to the fake claude at path.
func WriteFakeConfig(t *testing.T, path string, config FakeConfig) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "fakeclaude.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ReadCalls reads the invocations recorded in path.
func ReadCalls(t *testing.T, path string) []Call {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	calls := []Call{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var call Call
		if err := json.Unmarshal(scanner.Bytes(), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

func goTool() string {
	if path, err := exec.LookPath("go"); err == nil {
		return path
	}
	return filepath.Join(runtime.GOROOT(), "bin", "go")
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
