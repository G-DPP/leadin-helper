package host

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/protocol"
	"github.com/G-DPP/leadin-helper/internal/testutil"
	"github.com/G-DPP/leadin-helper/internal/version"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.Run(m))
}

// isolate gives the test an empty home and a PATH with only dir, so the real
// claude of the machine is never found.
func isolate(t *testing.T, dir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", "")
	t.Setenv("PATH", dir)
}

func request(t *testing.T, message map[string]any) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := protocol.WriteMessage(&buf, message); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func serve(t *testing.T, in io.Reader) (int, map[string]any, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "helper.log")
	var out bytes.Buffer
	code := Serve(in, &out, logs.New(logPath))
	var reply map[string]any
	if out.Len() > 0 {
		var err error
		if reply, err = protocol.ReadMessage(&out); err != nil {
			t.Fatalf("reading the reply: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("%d bytes after the reply", out.Len())
		}
	}
	data, _ := os.ReadFile(logPath)
	return code, reply, string(data)
}

func TestGetInfoWithClaude(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{
		Version: "2.1.300 (Claude Code)",
		Auth:    `{"loggedIn": true, "email": "someone@example.com", "subscriptionType": "max"}`,
	})
	isolate(t, filepath.Dir(path))
	code, reply, logText := serve(t, request(t, map[string]any{"action": "get_info"}))
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	want := map[string]any{
		"type":              "info",
		"helper_version":    version.Version,
		"protocol_version":  "2",
		"os":                runtime.GOOS,
		"arch":              runtime.GOARCH,
		"claude_found":      true,
		"claude_version":    "2.1.300",
		"logged_in":         true,
		"subscription_type": "max",
	}
	assertReply(t, reply, want)
	if !strings.HasSuffix(logText, " INFO action=get_info outcome=ok\n") {
		t.Fatalf("log = %q", logText)
	}
}

func TestGetInfoNeverContainsTheEmail(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{
		Version: "2.1.300",
		Auth:    `{"loggedIn": true, "email": "someone@example.com", "subscriptionType": "pro"}`,
	})
	isolate(t, filepath.Dir(path))
	var out bytes.Buffer
	logPath := filepath.Join(t.TempDir(), "helper.log")
	Serve(request(t, map[string]any{"action": "get_info"}), &out, logs.New(logPath))
	logText, _ := os.ReadFile(logPath)
	for _, text := range []string{out.String(), string(logText)} {
		if strings.Contains(text, "someone@example.com") || strings.Contains(text, "email") {
			t.Fatalf("email leaked: %q", text)
		}
	}
}

func TestGetInfoWithoutClaude(t *testing.T) {
	isolate(t, t.TempDir())
	code, reply, _ := serve(t, request(t, map[string]any{"action": "get_info"}))
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	assertReply(t, reply, map[string]any{
		"type":              "info",
		"helper_version":    version.Version,
		"protocol_version":  "2",
		"os":                runtime.GOOS,
		"arch":              runtime.GOARCH,
		"claude_found":      false,
		"claude_version":    nil,
		"logged_in":         false,
		"subscription_type": nil,
	})
}

func TestUnknownActionIsInvalidRequest(t *testing.T) {
	for _, message := range []map[string]any{{"action": "format_disk"}, {"action": 3}, {}} {
		code, reply, logText := serve(t, request(t, message))
		if code != 0 {
			t.Fatalf("%v: exit code %d", message, code)
		}
		assertReply(t, reply, map[string]any{"type": "error", "code": "invalid_request"})
		if !strings.Contains(logText, "action=invalid outcome=error error=invalid_request") || strings.Contains(logText, "format_disk") {
			t.Fatalf("log = %q", logText)
		}
	}
}

func TestInvalidMessageIsInvalidRequest(t *testing.T) {
	body := []byte("not json")
	frame := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	var tooLarge [4]byte
	binary.LittleEndian.PutUint32(tooLarge[:], protocol.MaxIncoming+1)
	for name, input := range map[string][]byte{"not json": frame, "above 4 MB": tooLarge[:], "truncated header": {1}} {
		code, reply, logText := serve(t, bytes.NewReader(input))
		if code != 0 {
			t.Fatalf("%s: exit code %d", name, code)
		}
		assertReply(t, reply, map[string]any{"type": "error", "code": "invalid_request"})
		if !strings.Contains(logText, " WARNING action=invalid outcome=error error=invalid_request") {
			t.Fatalf("%s: log = %q", name, logText)
		}
	}
}

func TestEmptyStreamExitsWithoutReply(t *testing.T) {
	code, reply, logText := serve(t, bytes.NewReader(nil))
	if code != 0 || reply != nil || logText != "" {
		t.Fatalf("code %d, reply %v, log %q", code, reply, logText)
	}
}

const testTicket = "tkt_" + "example0123456789abcdef"

const testToken = "tok_" + "example" + "0123456789abcdef"

func TestInvalidStartRunIsInvalidRequest(t *testing.T) {
	path := testutil.BuildFakeClaude(t)
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Version: "2.1.300", Auth: `{"loggedIn": true}`, Record: record})
	isolate(t, filepath.Dir(path))
	for _, message := range []map[string]any{
		{"action": "start_run", "run_id": 7, "ticket": testTicket, "server_url": "https://example.com", "mcp_token": testToken},
		{"action": "start_run", "run_id": 0, "ticket": testTicket, "server_url": "https://example.com"},
		{"action": "start_run", "run_id": 7, "ticket": "short", "server_url": "https://example.com"},
		{"action": "start_run", "run_id": 7, "ticket": testTicket, "server_url": "http://example.com"},
	} {
		code, reply, logText := serve(t, request(t, message))
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
		assertReply(t, reply, map[string]any{"type": "run_failed", "code": "invalid_request", "helper_version": version.Version})
		if !strings.HasSuffix(logText, " INFO action=start_run outcome=error error=invalid_request\n") {
			t.Fatalf("log = %q", logText)
		}
		for _, secret := range []string{testTicket, testToken, "example.com", "run_id"} {
			if strings.Contains(logText, secret) {
				t.Fatalf("%q in the log %q", secret, logText)
			}
		}
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("claude was called")
	}
}

func TestStartRunRunsClaudeAndLogsTheOutcome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs are checked on Unix")
	}
	path := testutil.BuildFakeClaude(t)
	fixture, _ := filepath.Abs(filepath.Join("..", "stream", "testdata", "run_basic.jsonl"))
	testutil.WriteFakeConfig(t, path, testutil.FakeConfig{Version: "2.1.300", Auth: `{"loggedIn": true, "subscriptionType": "pro"}`, Fixture: fixture})
	isolate(t, filepath.Dir(path))
	t.Setenv("TMPDIR", t.TempDir())
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run_id": 7, "mcp_url": server.URL + "/mcp", "mcp_token": testToken, "model": "sonnet",
			"prompt": "Call `start_example`.", "tools": []string{"WebSearch"},
			"progress_map": map[string]string{"store_item": "item_saved"},
			"outcomes":     []string{"done", "failed"}, "result_id_key": "item_id", "failure_message": "Failed.",
		})
	}))
	defer server.Close()

	// stdin stays open after the request until the run is over: its end
	// would cancel the run.
	pipeRead, pipeWrite := io.Pipe()
	defer pipeWrite.Close()
	in := io.MultiReader(request(t, map[string]any{"action": "start_run", "run_id": 7, "ticket": testTicket, "server_url": server.URL}), pipeRead)
	logPath := filepath.Join(t.TempDir(), "helper.log")
	var out bytes.Buffer
	if code := Serve(in, &out, logs.New(logPath)); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var types []string
	var last map[string]any
	for out.Len() > 0 {
		message, err := protocol.ReadMessage(&out)
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, message["type"].(string))
		last = message
	}
	if strings.Join(types, ",") != "run_progress,run_progress,run_progress,run_progress,run_finished" || last["outcome"] != "done" {
		t.Fatalf("types %v, last %v", types, last)
	}
	logText, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(logText)), "\n")
	final := lines[len(lines)-1]
	if !strings.Contains(final, " INFO action=start_run run_id=7 result_id=42 duration_ms=") || !strings.HasSuffix(final, " outcome=done") {
		t.Fatalf("log = %q", logText)
	}
	for _, secret := range []string{testTicket, testToken, "127.0.0.1", "start_example", "store_item"} {
		if strings.Contains(string(logText), secret) {
			t.Fatalf("%q in the log %q", secret, logText)
		}
	}
}

func TestPanicBecomesUnknown(t *testing.T) {
	actions["panic_for_test"] = func() map[string]any { panic("secret detail") }
	t.Cleanup(func() { delete(actions, "panic_for_test") })
	code, reply, logText := serve(t, request(t, map[string]any{"action": "panic_for_test"}))
	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	assertReply(t, reply, map[string]any{"type": "error", "code": "unknown"})
	if !strings.Contains(logText, " ERROR action=panic_for_test outcome=error error=panic") || strings.Contains(logText, "secret") {
		t.Fatalf("log = %q", logText)
	}
}

func assertReply(t *testing.T, reply, want map[string]any) {
	t.Helper()
	if len(reply) != len(want) {
		t.Fatalf("reply = %#v, want %#v", reply, want)
	}
	for key, value := range want {
		got := reply[key]
		// Numbers come back as json.Number.
		if number, ok := got.(interface{ String() string }); ok {
			got = number.String()
		}
		if got != value {
			t.Errorf("%s = %#v, want %#v", key, reply[key], value)
		}
	}
}
