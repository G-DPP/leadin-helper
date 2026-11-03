package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// isolate points every folder the installer uses to a temporary home.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TEMP", os.Getenv("TMPDIR"))
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	return home
}

// fakeBinary is a file standing for the downloaded helper.
func fakeBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leadin-helper_9.9.9_download")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManifestAllowsOnlyTheExtension(t *testing.T) {
	data, err := Manifest("/x/leadin-helper")
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	origins := manifest["allowed_origins"].([]any)
	if manifest["name"] != HostName || manifest["path"] != "/x/leadin-helper" || manifest["type"] != "stdio" ||
		len(origins) != 1 || origins[0] != "chrome-extension://fmkimdbalkokmkkpkapnmjjohhhcdpbm/" {
		t.Fatalf("manifest = %s", data)
	}
}

func TestInstallCopiesWithFixedNameAndRegisters(t *testing.T) {
	isolate(t)
	want := prepareBrowsers(t)
	var out bytes.Buffer
	if err := InstallFrom(fakeBinary(t, "v1"), &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	installed, _ := InstalledPath()
	if filepath.Base(installed) != ExecutableName() {
		t.Fatalf("installed as %s", installed)
	}
	if data, err := os.ReadFile(installed); err != nil || string(data) != "v1" {
		t.Fatalf("copy = %q, %v", data, err)
	}
	path, browsers := Status()
	if path != installed || !slices.Equal(browsers, want) {
		t.Fatalf("status = %q %v, want %q %v", path, browsers, installed, want)
	}
	assertManifestsPointTo(t, installed)
	if !strings.Contains(out.String(), "Installed: LeadIn helper") || !strings.Contains(out.String(), "You can go back to the browser.") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestSecondInstallOverwritesTheCopy(t *testing.T) {
	isolate(t)
	want := prepareBrowsers(t)
	if err := InstallFrom(fakeBinary(t, "v1"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := InstallFrom(fakeBinary(t, "v2"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	installed, _ := InstalledPath()
	if data, _ := os.ReadFile(installed); string(data) != "v2" {
		t.Fatalf("copy = %q", data)
	}
	if _, browsers := Status(); !slices.Equal(browsers, want) {
		t.Fatalf("browsers = %v", browsers)
	}
}

func TestInstallFromTheInstalledCopyKeepsIt(t *testing.T) {
	isolate(t)
	prepareBrowsers(t)
	if err := InstallFrom(fakeBinary(t, "v1"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	installed, _ := InstalledPath()
	if err := InstallFrom(installed, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "v1" {
		t.Fatalf("copy = %q", data)
	}
}

func TestInstallRemovesLegacyHosts(t *testing.T) {
	isolate(t)
	prepareBrowsers(t)
	for _, name := range legacyHostNames {
		if _, err := registerHost(name, []byte("{}"), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := InstallFrom(fakeBinary(t, "v1"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range legacyHostNames {
		if browsers := registeredBrowsers(name); len(browsers) != 0 {
			t.Errorf("%s still registered in %v", name, browsers)
		}
	}
}

func TestUninstallRemovesEverythingButTheLog(t *testing.T) {
	isolate(t)
	prepareBrowsers(t)
	if err := InstallFrom(fakeBinary(t, "v1"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(&out); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if path, browsers := Status(); path != "" || len(browsers) != 0 {
		t.Fatalf("after uninstall: %q %v", path, browsers)
	}
	dir, _ := BinDir()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("bin folder still there: %v", err)
	}
	out.Reset()
	if err := Uninstall(&out); err != nil || !strings.Contains(out.String(), "Nothing to remove") {
		t.Fatalf("second uninstall: %v %s", err, out.String())
	}
}

func TestMoveAsideAndCleanUp(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "leadin-helper")
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveAside(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("not moved")
	}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), oldCopyPrefix+"*"))
	if len(matches) != 1 {
		t.Fatalf("moved copies = %v", matches)
	}
	removeOldCopies()
	if matches, _ := filepath.Glob(filepath.Join(os.TempDir(), oldCopyPrefix+"*")); len(matches) != 0 {
		t.Fatalf("left = %v", matches)
	}
}
