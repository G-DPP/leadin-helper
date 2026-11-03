//go:build !windows

package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// configRoot is the folder with the browsers' profiles.
func configRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base, nil
	}
	return filepath.Join(home, ".config"), nil
}

type manifestTarget struct {
	browser string
	profile string
	path    string
}

// manifestTargets lists, for every known browser, its profile folder and the
// path of the manifest of host, whether the profile exists or not.
func manifestTargets(host string) ([]manifestTarget, error) {
	root, err := configRoot()
	if err != nil {
		return nil, err
	}
	browsers := linuxBrowsers
	if runtime.GOOS == "darwin" {
		browsers = macosBrowsers
	}
	targets := make([]manifestTarget, 0, len(browsers))
	for _, b := range browsers {
		profile := filepath.Join(root, filepath.FromSlash(b.profile))
		targets = append(targets, manifestTarget{b.name, profile, filepath.Join(profile, nativeHostsDir, host+".json")})
	}
	return targets, nil
}

// registerHost writes the manifest into every browser profile that exists.
func registerHost(host string, manifest []byte, out io.Writer) (int, error) {
	targets, err := manifestTargets(host)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, target := range targets {
		if info, err := os.Stat(target.profile); err != nil || !info.IsDir() {
			fmt.Fprintf(out, "Not found   %s\n", target.browser)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
			return written, fmt.Errorf("cannot create %s: %w", filepath.Dir(target.path), err)
		}
		if err := os.WriteFile(target.path, manifest, 0o644); err != nil {
			return written, fmt.Errorf("cannot write %s: %w", target.path, err)
		}
		fmt.Fprintf(out, "Registered  %s (%s)\n", target.browser, target.path)
		written++
	}
	return written, nil
}

// unregisterHost removes the manifest of host from every browser profile.
func unregisterHost(host string, out io.Writer) (int, error) {
	targets, err := manifestTargets(host)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, target := range targets {
		err := os.Remove(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("cannot remove %s: %w", target.path, err)
		}
		fmt.Fprintf(out, "Removed     %s (%s)\n", target.path, target.browser)
		removed++
	}
	return removed, nil
}

// registeredBrowsers are the browsers with a manifest of host.
func registeredBrowsers(host string) []string {
	targets, err := manifestTargets(host)
	if err != nil {
		return nil
	}
	browsers := []string{}
	for _, target := range targets {
		if _, err := os.Stat(target.path); err == nil {
			browsers = append(browsers, target.browser)
		}
	}
	return browsers
}
