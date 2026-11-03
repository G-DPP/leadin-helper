package host

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/protocol"
	"github.com/G-DPP/leadin-helper/internal/testutil"
)

// events serves one request and returns every message written.
func events(t *testing.T, message map[string]any) ([]map[string]any, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "helper.log")
	var out bytes.Buffer
	// stdin stays open, as with Chrome: its end would cancel the step.
	open := make(chan struct{})
	t.Cleanup(func() { close(open) })
	in := io.MultiReader(request(t, message), blockingReader(open))
	if code := Serve(in, &out, logs.New(logPath)); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	messages := []map[string]any{}
	for out.Len() > 0 {
		message, err := protocol.ReadMessage(&out)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	data, _ := os.ReadFile(logPath)
	return messages, string(data)
}

type blockingReader chan struct{}

func (b blockingReader) Read([]byte) (int, error) {
	<-b
	return 0, io.EOF
}

func TestUnknownSetupStepIsInvalidRequest(t *testing.T) {
	isolate(t, t.TempDir())
	messages, logText := events(t, map[string]any{"action": "run_setup_step", "step": "rm_rf"})
	if len(messages) != 1 || messages[0]["type"] != "setup_finished" || messages[0]["error_code"] != "invalid_request" ||
		messages[0]["step"] != "" || messages[0]["exit_code"] != nil {
		t.Fatalf("messages = %v", messages)
	}
	if !strings.Contains(logText, "action=run_setup_step outcome=error error=invalid_request") {
		t.Fatalf("log = %q", logText)
	}
}

func TestSetupStepStreamsStartOutputAndEnd(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Version: "2.1.300 (Claude Code)", Auth: `{"loggedIn": true}`})
	isolate(t, filepath.Dir(path))
	messages, logText := events(t, map[string]any{"action": "run_setup_step", "step": "logout_claude"})
	types := []any{}
	for _, message := range messages {
		types = append(types, message["type"])
	}
	if len(messages) < 3 || types[0] != "setup_started" || types[len(types)-1] != "setup_finished" ||
		messages[0]["command"] != "claude auth logout" || messages[1]["type"] != "setup_output" {
		t.Fatalf("messages = %v", messages)
	}
	last := messages[len(messages)-1]
	if last["error_code"] != "" || last["exit_code"].(interface{ String() string }).String() != "0" {
		t.Fatalf("last = %v", last)
	}
	if !strings.Contains(logText, "action=run_setup_step") || !strings.Contains(logText, "step=logout_claude") {
		t.Fatalf("log = %q", logText)
	}
}

func TestDiagnosticsHasVersionsButNoEmail(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{
		Version: "2.1.300 (Claude Code)",
		Auth:    `{"loggedIn": true, "email": "someone@example.com", "subscriptionType": "pro"}`,
	})
	isolate(t, filepath.Dir(path))
	messages, _ := events(t, map[string]any{"action": "diagnostics"})
	text, _ := messages[0]["text"].(string)
	if messages[0]["type"] != "diagnostics" || !strings.Contains(text, "claude_version: \"2.1.300\"") ||
		!strings.Contains(text, "account: \"current\"") || strings.Contains(text, "someone@example.com") {
		t.Fatalf("diagnostics = %q", text)
	}
}
