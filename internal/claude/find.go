// Package claude finds the claude executable and reads its version and login
// state.
//
// Chrome starts the helper with a reduced PATH (especially on macOS): besides
// PATH, the places where the installer and npm put claude are checked too.
//
// The output of `claude auth status` contains the user's email address: it
// never leaves this package. Only loggedIn and subscriptionType go out.
package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// KnownPaths are the usual places of claude, computed on the current home.
func KnownPaths() []string {
	home, _ := os.UserHomeDir()
	return knownPaths(runtime.GOOS, home, os.Getenv)
}

func knownPaths(goos, home string, getenv func(string) string) []string {
	if goos == "windows" {
		profile := getenv("USERPROFILE")
		if profile == "" {
			profile = home
		}
		paths := []string{filepath.Join(profile, ".local", "bin", "claude.exe")}
		if appdata := getenv("APPDATA"); appdata != "" {
			paths = append(paths, filepath.Join(appdata, "npm", "claude.cmd"))
		}
		return paths
	}
	return []string{
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".claude", "local", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
		filepath.Join(home, ".npm-global", "bin", "claude"),
	}
}

// Find returns the path of claude, or "" if there is none. It looks first in
// the PATH of env, the environment claude will start with, then in
// KnownPaths.
func Find(env []string) string {
	return find(env, KnownPaths())
}

// FindWith is Find, but a path chosen by the person (the setup page's
// Advanced panel) comes first, if it is still there.
func FindWith(env []string, chosen string) string {
	if chosen != "" && isExecutable(chosen) {
		return chosen
	}
	return Find(env)
}

func find(env []string, known []string) string {
	if found := lookPath(env); found != "" {
		return found
	}
	for _, path := range known {
		if isExecutable(path) {
			return path
		}
	}
	return ""
}

// lookPath searches claude in the PATH of env (not of the helper), trying the
// PATHEXT extensions on Windows.
func lookPath(env []string) string {
	names := []string{"claude"}
	if runtime.GOOS == "windows" {
		names = nil
		pathext := lookupEnv(env, "PATHEXT")
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		for _, ext := range strings.Split(pathext, ";") {
			if ext != "" {
				names = append(names, "claude"+strings.ToLower(ext))
			}
		}
	}
	for _, dir := range filepath.SplitList(lookupEnv(env, "PATH")) {
		if dir == "" {
			continue
		}
		for _, name := range names {
			if path := filepath.Join(dir, name); isExecutable(path) {
				return path
			}
		}
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}
