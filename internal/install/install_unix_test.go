//go:build !windows

package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareBrowsers creates the profiles of Chrome and Brave only, and returns
// the browsers that must end up registered.
func prepareBrowsers(t *testing.T) []string {
	t.Helper()
	root, err := configRoot()
	if err != nil {
		t.Fatal(err)
	}
	browsers := linuxBrowsers
	if strings.Contains(root, "Library") {
		browsers = macosBrowsers
	}
	for _, b := range browsers {
		if b.name == "Chrome" || b.name == "Brave" {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(b.profile)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return []string{"Brave", "Chrome"}
}

func assertManifestsPointTo(t *testing.T, installed string) {
	t.Helper()
	targets, _ := manifestTargets(HostName)
	for _, target := range targets {
		data, err := os.ReadFile(target.path)
		if target.browser != "Chrome" && target.browser != "Brave" {
			if err == nil {
				t.Errorf("manifest for missing browser %s", target.browser)
			}
			continue
		}
		var manifest map[string]any
		if err := json.Unmarshal(data, &manifest); err != nil || manifest["path"] != installed {
			t.Errorf("%s manifest = %s (%v)", target.browser, data, err)
		}
	}
}

func TestNoBrowserIsAnError(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	err := InstallFrom(fakeBinary(t, "v1"), &out)
	if err == nil || !strings.Contains(err.Error(), "no Chromium browser found") {
		t.Fatalf("err = %v", err)
	}
}
