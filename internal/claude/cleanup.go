package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// ConfigDir is the Claude Code configuration folder of the runs: always
// ~/.claude, since RunEnvironment drops CLAUDE_CONFIG_DIR.
func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// dirName is the folder name Claude Code derives from a path: every character
// that is not an ASCII letter or digit becomes "-".
func dirName(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// pathNames are the folder names of workdir and of its real path (on macOS
// /var/… is /private/var/…), sorted and without duplicates.
func pathNames(workdir string) []string {
	names := map[string]bool{dirName(workdir): true}
	if real, err := filepath.EvalSymlinks(workdir); err == nil {
		names[dirName(real)] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return sorted
}

// ProjectDirs are the folders Claude Code creates in <configDir>/projects/
// for workdir. --no-session-persistence avoids the transcript, but the
// project folder is created anyway, and WebFetch saves the large files it
// downloads there (<session>/tool-results/).
func ProjectDirs(workdir, configDir string) []string {
	dirs := []string{}
	for _, name := range pathNames(workdir) {
		dirs = append(dirs, filepath.Join(configDir, "projects", name))
	}
	return dirs
}

// TaskOutputDirs are the folders Claude Code creates in $TMPDIR/claude-<uid>/
// for workdir: the output files of background subagents go there. They stay
// empty, but they belong to the run. None on Windows.
func TaskOutputDirs(workdir string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	base := filepath.Join(os.TempDir(), "claude-"+strconv.Itoa(os.Getuid()))
	dirs := []string{}
	for _, name := range pathNames(workdir) {
		dirs = append(dirs, filepath.Join(base, name))
	}
	return dirs
}

// RemoveRunDirs removes the folders of ProjectDirs and TaskOutputDirs: they
// belong to this run only. Errors are ignored.
func RemoveRunDirs(workdir, configDir string) {
	for _, dir := range append(ProjectDirs(workdir, configDir), TaskOutputDirs(workdir)...) {
		_ = os.RemoveAll(dir)
	}
}
