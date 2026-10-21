package host

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"leadin-helper/internal/logs"
	"leadin-helper/internal/protocol"
	"leadin-helper/internal/testutil"
	"leadin-helper/internal/version"
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

func TestStartRunFailsCleanlyForNow(t *testing.T) {
	code, reply, logText := serve(t, request(t, map[string]any{
		"action": "start_run", "run_id": 7, "ticket": strings.Repeat("a", 32), "server_url": "https://example.com",
	}))
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	assertReply(t, reply, map[string]any{"type": "run_failed", "code": "unknown", "helper_version": version.Version})
	if !strings.Contains(logText, "action=start_run outcome=error error=not_implemented") || strings.Contains(logText, "example.com") {
		t.Fatalf("log = %q", logText)
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
