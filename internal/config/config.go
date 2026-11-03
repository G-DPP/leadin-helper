// Package config holds the person's choices for the runs, saved by the
// set_claude_config setup step: which Claude Code account the runs use and,
// if it is not found by itself, where claude is.
//
// Accounts:
//
//   - current: the person's usual Claude Code login (~/.claude).
//   - profile: a login made only for LeadIn, in a fixed folder of the helper
//     (CLAUDE_CONFIG_DIR). Fixed, never temporary: on macOS its Keychain
//     entry outlives the folder (F0 results).
//   - api_key: an Anthropic API key (ANTHROPIC_API_KEY), kept in a file
//     readable only by the user. It never goes to the log, get_info or
//     diagnostics.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/G-DPP/leadin-helper/internal/logs"
)

const (
	Current = "current"
	Profile = "profile"
	APIKey  = "api_key"
)

// Accounts are the accepted values of Config.Account.
var Accounts = []string{Current, Profile, APIKey}

// apiKey is the shape of a key: no spaces or quotes, it goes into an
// environment variable.
var apiKey = regexp.MustCompile(`^[A-Za-z0-9_-]{20,300}$`)

// Config is the content of config.json.
type Config struct {
	Account    string `json:"account"`
	ClaudePath string `json:"claude_path,omitempty"`
}

func dataFile(name string) (string, error) {
	dir, err := logs.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// ProfileDir is the CLAUDE_CONFIG_DIR of the profile account.
func ProfileDir() (string, error) { return dataFile("claude-profile") }

// Load reads config.json; a missing or unreadable file is the default
// (current account, claude found by itself).
func Load() Config {
	config := Config{Account: Current}
	path, err := dataFile("config.json")
	if err != nil {
		return config
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config
	}
	var saved Config
	if json.Unmarshal(data, &saved) != nil || !Valid(saved) {
		return config
	}
	return saved
}

// Valid checks the account and the claude path.
func Valid(config Config) bool {
	known := false
	for _, account := range Accounts {
		known = known || config.Account == account
	}
	return known && ValidClaudePath(config.ClaudePath)
}

// ValidClaudePath accepts "" (find it) or an absolute path to a file.
func ValidClaudePath(path string) bool {
	if path == "" {
		return true
	}
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\n\r") {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// ValidAPIKey checks the shape of a key.
func ValidAPIKey(key string) bool { return apiKey.MatchString(key) }

// Save writes config.json.
func Save(config Config) error {
	if !Valid(config) {
		return errors.New("invalid configuration")
	}
	path, err := dataFile("config.json")
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, append(data, '\n'))
}

// SetAPIKey saves the key, readable by the user only.
func SetAPIKey(key string) error {
	if !ValidAPIKey(key) {
		return errors.New("invalid API key")
	}
	path, err := dataFile("api_key")
	if err != nil {
		return err
	}
	return writePrivate(path, []byte(key+"\n"))
}

// StoredAPIKey is the saved key, or "".
func StoredAPIKey() string {
	path, err := dataFile("api_key")
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	key := strings.TrimSpace(string(data))
	if !ValidAPIKey(key) {
		return ""
	}
	return key
}

// RemoveAPIKey deletes the saved key, if any.
func RemoveAPIKey() error {
	path, err := dataFile("api_key")
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Apply adds the account to env, claude's environment: CLAUDE_CONFIG_DIR for
// the profile, ANTHROPIC_API_KEY for the key. env never has them already:
// the allowlist of the run environment drops every inherited one.
func (c Config) Apply(env []string) ([]string, error) {
	switch c.Account {
	case Profile:
		dir, err := ProfileDir()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		return append(env, "CLAUDE_CONFIG_DIR="+dir), nil
	case APIKey:
		key := StoredAPIKey()
		if key == "" {
			return nil, errors.New("no API key saved")
		}
		return append(env, "ANTHROPIC_API_KEY="+key), nil
	}
	return env, nil
}

// ClaudeConfigDir is the Claude Code configuration folder the runs use, for
// the cleanup at the end of a run.
func (c Config) ClaudeConfigDir() string {
	if c.Account == Profile {
		if dir, err := ProfileDir(); err == nil {
			return dir
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
