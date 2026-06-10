// Package channelsetup makes cc-review an approved Claude channel so its
// channel server loads without the "Loading development channels" confirmation.
// It merges two settings files: the machine-wide managed-settings.json (the only
// place Claude reads allowedChannelPlugins from, so the plugin counts as approved
// rather than development) and the user's settings.json (records the channel
// selection). The managed write needs root, so it goes through a macOS admin prompt.
package channelsetup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Marketplace and Plugin identify cc-review's channel; they must match the
// top-level marketplace.json name and plugin.json name. ChannelID is the
// `plugin:<name>@<marketplace>` form Claude uses for --channels.
const (
	Marketplace = "cc-review"
	Plugin      = "review"
	ChannelID   = "plugin:" + Plugin + "@" + Marketplace
)

// ManagedSettingsPath is the machine-wide managed-settings.json Claude reads
// allowedChannelPlugins from.
func ManagedSettingsPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return `C:\Program Files\ClaudeCode\managed-settings.json`
	default:
		return "/etc/claude-code/managed-settings.json"
	}
}

// UserSettingsPath is the user's Claude settings.json, honoring CLAUDE_CONFIG_DIR.
func UserSettingsPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("resolve home dir: %v", err))
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// MergeManaged returns managed-settings.json with channelsEnabled set and
// cc-review present in allowedChannelPlugins, preserving every other key. It is
// idempotent: re-merging an already-approved file is a no-op apart from
// re-serialization.
func MergeManaged(existing []byte) ([]byte, error) {
	m, err := decodeObject(existing, "managed settings")
	if err != nil {
		return nil, err
	}
	m["channelsEnabled"] = true
	list, _ := m["allowedChannelPlugins"].([]any)
	if !allowlistHasEntry(list) {
		list = append(list, map[string]any{"marketplace": Marketplace, "plugin": Plugin})
	}
	m["allowedChannelPlugins"] = list
	return encodeObject(m)
}

// ManagedHasEntry reports whether cc-review is already in allowedChannelPlugins.
func ManagedHasEntry(existing []byte) (bool, error) {
	m, err := decodeObject(existing, "managed settings")
	if err != nil {
		return false, err
	}
	list, _ := m["allowedChannelPlugins"].([]any)
	return allowlistHasEntry(list), nil
}

// MergeUserSettings returns settings.json with cc-review's channel id in the
// channels array, preserving every other key. Idempotent.
func MergeUserSettings(existing []byte) ([]byte, error) {
	m, err := decodeObject(existing, "user settings")
	if err != nil {
		return nil, err
	}
	channels, _ := m["channels"].([]any)
	if !containsString(channels, ChannelID) {
		channels = append(channels, ChannelID)
	}
	m["channels"] = channels
	return encodeObject(m)
}

// ApplyManagedViaAdmin writes merged to ManagedSettingsPath as root. On macOS it
// stages a temp file and copies it into place through an osascript admin prompt;
// elsewhere it leaves the temp file and returns the sudo command to run.
func ApplyManagedViaAdmin(merged []byte) error {
	dest := ManagedSettingsPath()
	tmp, err := os.CreateTemp("", "cc-review-managed-*.json")
	if err != nil {
		return fmt.Errorf("create temp settings: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp settings: %w", err)
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic managed-settings write is macOS-only; run: sudo install -d -m 755 %q && sudo install -m 644 %q %q", filepath.Dir(dest), tmpPath, dest)
	}
	defer os.Remove(tmpPath)
	shellCmd := fmt.Sprintf("mkdir -p '%s' && cp '%s' '%s' && chmod 644 '%s'", filepath.Dir(dest), tmpPath, dest, dest)
	script := `do shell script "` + shellCmd + `" with administrator privileges`
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("admin write of %s (%s): %w", dest, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func decodeObject(b []byte, what string) (map[string]any, error) {
	m := map[string]any{}
	if len(b) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", what, err)
	}
	return m, nil
}

func encodeObject(m map[string]any) ([]byte, error) {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func allowlistHasEntry(list []any) bool {
	for _, e := range list {
		obj, ok := e.(map[string]any)
		if ok && obj["marketplace"] == Marketplace && obj["plugin"] == Plugin {
			return true
		}
	}
	return false
}

func containsString(list []any, want string) bool {
	for _, e := range list {
		if s, ok := e.(string); ok && s == want {
			return true
		}
	}
	return false
}
