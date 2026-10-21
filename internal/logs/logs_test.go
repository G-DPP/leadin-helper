package logs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logs", "helper.log")
	logger := New(path)
	logger.now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.FixedZone("CEST", 2*3600)) }
	return logger, path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEventWritesTimeLevelAndFields(t *testing.T) {
	logger, path := newTestLogger(t)
	logger.Event(Info, map[string]any{"action": "get_info", "outcome": "ok"})
	if got, want := readLog(t, path), "2026-10-20T10:00:00Z INFO action=get_info outcome=ok\n"; got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

func TestKeysOutsideTheWhitelistAreDropped(t *testing.T) {
	logger, path := newTestLogger(t)
	logger.Event(Warning, map[string]any{
		"action":     "start_run",
		"prompt":     "secret prompt",
		"token":      "secret-token",
		"email":      "someone@example.com",
		"server_url": "https://example.com",
	})
	text := readLog(t, path)
	for _, secret := range []string{"prompt", "secret", "token", "email", "example.com", "server_url"} {
		if strings.Contains(text, secret) {
			t.Errorf("log contains %q: %q", secret, text)
		}
	}
	if !strings.HasSuffix(text, " WARNING action=start_run\n") {
		t.Fatalf("log = %q", text)
	}
}

func TestFieldsFollowTheWhitelistOrder(t *testing.T) {
	got := Format(map[string]any{
		"error": "timeout", "outcome": "error", "duration_ms": 12, "key": "working",
		"result_id": 5, "run_id": 7, "action": "start_run",
	})
	want := "action=start_run run_id=7 result_id=5 key=working duration_ms=12 outcome=error error=timeout"
	if got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
}

func TestLogRotatesAboveOneMegabyte(t *testing.T) {
	logger, path := newTestLogger(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= Backups+1; i++ {
		// A full log file, then one more line.
		if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxBytes)), 0o644); err != nil {
			t.Fatal(err)
		}
		logger.Event(Info, map[string]any{"action": "get_info"})
	}
	if text := readLog(t, path); strings.Count(text, "\n") != 1 || !strings.Contains(text, "action=get_info") {
		t.Fatalf("current log = %q", text)
	}
	for i := 1; i <= Backups; i++ {
		info, err := os.Stat(path + "." + strconv.Itoa(i))
		if err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
		if info.Size() < MaxBytes {
			t.Errorf("backup %d has %d bytes", i, info.Size())
		}
	}
	if _, err := os.Stat(path + ".4"); !os.IsNotExist(err) {
		t.Fatalf("a fourth backup exists: %v", err)
	}
}

func TestUnwritableLogIsIgnored(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The log folder would be inside a regular file: it cannot be created.
	New(filepath.Join(blocker, "logs", "helper.log")).Event(Info, map[string]any{"action": "get_info"})
	var zero *Logger
	zero.Event(Info, map[string]any{"action": "get_info"})
	(&Logger{}).Event(Info, map[string]any{"action": "get_info"})
}

func TestDataDirPerOS(t *testing.T) {
	home := filepath.Join("home", "someone")
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join(home, ".local", "share", "leadin")},
		{"linux", map[string]string{"XDG_DATA_HOME": filepath.Join("xdg", "data")}, filepath.Join("xdg", "data", "leadin")},
		{"darwin", nil, filepath.Join(home, "Library", "Application Support", "LeadIn")},
		{"windows", nil, filepath.Join(home, "AppData", "Local", "LeadIn")},
		{"windows", map[string]string{"LOCALAPPDATA": filepath.Join("local")}, filepath.Join("local", "LeadIn")},
	}
	for _, c := range cases {
		if got := dataDir(c.goos, home, env(c.env)); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestPathUsesTheRealEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, home) || !regexp.MustCompile(`helper\.log$`).MatchString(path) {
		t.Fatalf("Path = %q", path)
	}
}
