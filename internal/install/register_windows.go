package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"

	"github.com/G-DPP/leadin-helper/internal/logs"
)

// registryKeys are the HKCU keys under which each browser looks for native
// hosts. Brave and Vivaldi read Chrome's. A variable so the tests can use
// keys of their own.
var registryKeys = []browser{
	{"Chrome", `Software\Google\Chrome`},
	{"Chromium", `Software\Chromium`},
	{"Edge", `Software\Microsoft\Edge`},
}

// manifestPath is where the manifest of host lives on Windows: the registry
// keys point to it.
func manifestPath(host string) (string, error) {
	dir, err := logs.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "native_host", host+".json"), nil
}

func hostKey(base, host string) string {
	return base + `\` + nativeHostsDir + `\` + host
}

// registerHost writes the manifest once and points every browser's key to it.
// The keys are written even if a browser is not installed: they are harmless,
// and a browser installed later finds the helper.
func registerHost(host string, manifest []byte, out io.Writer) (int, error) {
	path, err := manifestPath(host)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("cannot create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, manifest, 0o644); err != nil {
		return 0, fmt.Errorf("cannot write %s: %w", path, err)
	}
	fmt.Fprintf(out, "Manifest    %s\n", path)
	written := 0
	for _, b := range registryKeys {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, hostKey(b.profile, host), registry.SET_VALUE)
		if err != nil {
			return written, fmt.Errorf("cannot create HKCU\\%s: %w", hostKey(b.profile, host), err)
		}
		err = key.SetStringValue("", path)
		key.Close()
		if err != nil {
			return written, fmt.Errorf("cannot write HKCU\\%s: %w", hostKey(b.profile, host), err)
		}
		fmt.Fprintf(out, "Registered  %s (HKCU\\%s)\n", b.name, hostKey(b.profile, host))
		written++
	}
	return written, nil
}

func unregisterHost(host string, out io.Writer) (int, error) {
	removed := 0
	for _, b := range registryKeys {
		err := registry.DeleteKey(registry.CURRENT_USER, hostKey(b.profile, host))
		if errors.Is(err, registry.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("cannot remove HKCU\\%s: %w", hostKey(b.profile, host), err)
		}
		fmt.Fprintf(out, "Removed     HKCU\\%s (%s)\n", hostKey(b.profile, host), b.name)
		removed++
	}
	path, err := manifestPath(host)
	if err != nil {
		return removed, err
	}
	if err := os.Remove(path); err == nil {
		fmt.Fprintf(out, "Removed     %s\n", path)
		removed++
		os.Remove(filepath.Dir(path)) // only if empty
	} else if !errors.Is(err, os.ErrNotExist) {
		return removed, fmt.Errorf("cannot remove %s: %w", path, err)
	}
	return removed, nil
}

// registeredBrowsers are the browsers whose key points to an existing
// manifest. Brave and Vivaldi follow Chrome.
func registeredBrowsers(host string) []string {
	browsers := []string{}
	for _, b := range registryKeys {
		key, err := registry.OpenKey(registry.CURRENT_USER, hostKey(b.profile, host), registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := key.GetStringValue("")
		key.Close()
		if err != nil {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		browsers = append(browsers, b.name)
		if b.name == "Chrome" {
			browsers = append(browsers, "Brave", "Vivaldi")
		}
	}
	return browsers
}
