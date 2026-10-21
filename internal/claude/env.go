package claude

import (
	"runtime"
	"strings"
)

// AllowedEnv are the environment variables passed on to claude (see
// RunEnvironment).
var AllowedEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG",
	"TMPDIR", "TMP", "TEMP",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS",
	// Windows. Many programs need SystemDrive and windir to start.
	"USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "ComSpec", "PATHEXT",
	"SystemDrive", "windir",
}

// AllowedEnvPrefixes are the prefixes of the other variables passed on.
var AllowedEnvPrefixes = []string{"LC_"}

// RunEnvironment is claude's environment: only the allowed variables of
// environ ("NAME=value" entries).
//
// An allowlist, not a denylist: no claude flag blocks the person's
// environment variables, and an ANTHROPIC_API_KEY left in a shell would
// override the chosen login. So every inherited ANTHROPIC_* and CLAUDE_*
// variable stays out, CLAUDE_CONFIG_DIR included: the configuration folder is
// always the default one.
//
// Variable names are case-insensitive on Windows.
func RunEnvironment(environ []string) []string {
	return runEnvironment(environ, runtime.GOOS == "windows")
}

func runEnvironment(environ []string, foldCase bool) []string {
	normalize := func(name string) string { return name }
	if foldCase {
		normalize = strings.ToUpper
	}
	allowed := make(map[string]bool, len(AllowedEnv))
	for _, name := range AllowedEnv {
		allowed[normalize(name)] = true
	}
	kept := []string{}
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if !found || name == "" {
			continue
		}
		if allowed[normalize(name)] || hasAllowedPrefix(normalize(name), normalize) {
			kept = append(kept, entry)
		}
	}
	return kept
}

func hasAllowedPrefix(name string, normalize func(string) string) bool {
	for _, prefix := range AllowedEnvPrefixes {
		if strings.HasPrefix(name, normalize(prefix)) {
			return true
		}
	}
	return false
}

// lookupEnv is the value of name in env, case-insensitive on Windows.
func lookupEnv(env []string, name string) string {
	value := ""
	for _, entry := range env {
		key, rest, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if key == name || (runtime.GOOS == "windows" && strings.EqualFold(key, name)) {
			// The last one wins, as in os/exec.
			value = rest
		}
	}
	return value
}
