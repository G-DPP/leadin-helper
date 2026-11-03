// Package install copies the helper into a fixed folder of the user and
// registers it as the native messaging host of the LeadIn extension in every
// Chromium browser it finds. No administrator password: only the user's
// folders and, on Windows, the user's registry (HKCU).
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/version"
)

const (
	// HostName is the native messaging host the extension connects to.
	HostName        = "com.leadin.helper"
	hostDescription = "LeadIn helper"
	// ExtensionID is the ID of the LeadIn extension, fixed by the key in its
	// manifest: the only caller the host accepts.
	ExtensionID = "fmkimdbalkokmkkpkapnmjjohhhcdpbm"
	// nativeHostsDir is the folder of a browser profile with the manifests.
	nativeHostsDir = "NativeMessagingHosts"
)

// legacyHostNames are earlier hosts that registering removes: the first
// LeadIn helper and the one of the F0 spikes.
var legacyHostNames = []string{"com.leadin.call_helper", "com.leadin.helper_spike"}

// browser is a Chromium browser and the folder of its profile, relative to
// the user's configuration folder (Linux, macOS).
type browser struct {
	name    string
	profile string
}

var linuxBrowsers = []browser{
	{"Chrome", "google-chrome"},
	{"Chromium", "chromium"},
	{"Edge", "microsoft-edge"},
	{"Brave", "BraveSoftware/Brave-Browser"},
	{"Vivaldi", "vivaldi"},
}

var macosBrowsers = []browser{
	{"Chrome", "Google/Chrome"},
	{"Chromium", "Chromium"},
	{"Edge", "Microsoft Edge"},
	{"Brave", "BraveSoftware/Brave-Browser"},
	{"Vivaldi", "Vivaldi"},
}

// ExecutableName is the fixed name of the installed copy, whatever the name
// of the file that was launched (on macOS an unzipped file next to one with
// the same name becomes "… 2", F0 results).
func ExecutableName() string {
	if runtime.GOOS == "windows" {
		return "leadin-helper.exe"
	}
	return "leadin-helper"
}

// BinDir is the folder of the installed copy.
func BinDir() (string, error) {
	dir, err := logs.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bin"), nil
}

// InstalledPath is the path of the installed copy, whether it exists or not.
func InstalledPath() (string, error) {
	dir, err := BinDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ExecutableName()), nil
}

// Manifest is the native host manifest for the executable at path.
func Manifest(path string) ([]byte, error) {
	data, err := json.MarshalIndent(map[string]any{
		"name":            HostName,
		"description":     hostDescription,
		"path":            path,
		"type":            "stdio",
		"allowed_origins": []string{"chrome-extension://" + ExtensionID + "/"},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Install copies the running executable into BinDir and registers it.
func Install(out io.Writer) error {
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find the helper's own file: %w", err)
	}
	return InstallFrom(source, out)
}

// InstallFrom copies source into BinDir, overwriting an earlier version, and
// registers the copy in every browser found.
func InstallFrom(source string, out io.Writer) error {
	fmt.Fprintf(out, "%s\n\n", version.String())
	removeOldCopies()
	target, err := InstalledPath()
	if err != nil {
		return err
	}
	if err := copyExecutable(source, target); err != nil {
		return fmt.Errorf("cannot copy the helper to %s: %w", target, err)
	}
	fmt.Fprintf(out, "Copied to   %s\n", target)

	manifest, err := Manifest(target)
	if err != nil {
		return err
	}
	written, err := registerHost(HostName, manifest, out)
	if err != nil {
		return err
	}
	for _, name := range legacyHostNames {
		if _, err := unregisterHost(name, io.Discard); err != nil {
			return err
		}
	}
	removeLegacyLaunchers()
	if written == 0 {
		return errors.New("no Chromium browser found (Chrome, Chromium, Edge, Brave, Vivaldi): open the browser once, then try again")
	}
	fmt.Fprintf(out, "\nInstalled: LeadIn helper %s is ready. You can go back to the browser.\n", version.Version)
	return nil
}

// Uninstall removes manifests, registry keys and the installed copy. The log
// stays.
func Uninstall(out io.Writer) error {
	removed := 0
	for _, name := range append([]string{HostName}, legacyHostNames...) {
		n, err := unregisterHost(name, out)
		if err != nil {
			return err
		}
		removed += n
	}
	dir, err := BinDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		if err := removeBinDir(dir); err != nil {
			return err
		}
		fmt.Fprintf(out, "Removed     %s\n", dir)
		removed++
	}
	if removed == 0 {
		fmt.Fprintln(out, "Nothing to remove: the LeadIn helper is not installed.")
		return nil
	}
	fmt.Fprintln(out, "\nUninstalled: the LeadIn helper is no longer registered.")
	return nil
}

// Status is what get_info reports about the installation, read when asked:
// the registration can disappear after the install (P7 of the F0 results).
func Status() (installPath string, browsers []string) {
	path, err := InstalledPath()
	if err == nil {
		if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
			path = ""
		}
	} else {
		path = ""
	}
	browsers = registeredBrowsers(HostName)
	sort.Strings(browsers)
	return path, browsers
}

// copyExecutable writes target as a new file. If target is the running
// executable there is nothing to copy. An existing target that cannot be
// removed (on Windows, a copy Chrome is running) is moved aside first.
func copyExecutable(source, target string) error {
	same, err := sameFile(source, target)
	if err != nil {
		return err
	}
	if same {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		if moveErr := moveAside(target); moveErr != nil {
			return err
		}
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, in); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(target, 0o755)
}

func sameFile(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	infoB, err := os.Stat(b)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(infoA, infoB), nil
}

// oldCopyPrefix names the copies moved aside into the temporary folder.
const oldCopyPrefix = "leadin-helper-old-"

// moveAside moves path into the user's temporary folder, where the next
// install deletes it. Windows can rename a running executable but not delete
// it (P4 of the F0 results).
func moveAside(path string) error {
	file, err := os.CreateTemp("", oldCopyPrefix+"*"+filepath.Ext(path))
	if err != nil {
		return err
	}
	name := file.Name()
	file.Close()
	if err := os.Remove(name); err != nil {
		return err
	}
	return os.Rename(path, name)
}

// legacyLaunchers are the scripts the first LeadIn helper registered
// instead of a binary, in <data>/native_host/.
var legacyLaunchers = []string{"leadin-helper.sh", "leadin-helper.bat", "leadin-call-helper.sh", "leadin-call-helper.bat"}

func removeLegacyLaunchers() {
	dir, err := logs.DataDir()
	if err != nil {
		return
	}
	for _, name := range legacyLaunchers {
		os.Remove(filepath.Join(dir, "native_host", name))
	}
}

// removeOldCopies deletes the copies moved aside by earlier runs, if they are
// no longer running.
func removeOldCopies() {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), oldCopyPrefix+"*"))
	for _, match := range matches {
		os.Remove(match)
	}
}

// removeBinDir removes the folder of the installed copy, moving the running
// executable aside if it is in there.
func removeBinDir(dir string) error {
	err := os.RemoveAll(dir)
	if err == nil {
		return nil
	}
	if self, exeErr := os.Executable(); exeErr == nil && strings.HasPrefix(filepath.Clean(self), filepath.Clean(dir)+string(filepath.Separator)) {
		if moveErr := moveAside(self); moveErr == nil {
			if err = os.RemoveAll(dir); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("cannot remove %s (%v): delete the folder by hand", dir, err)
}
