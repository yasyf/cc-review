package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// mustCwd returns cwd, defaulting to the process working directory.
func mustCwd(cwd string) string {
	if cwd != "" {
		return cwd
	}
	d, _ := os.Getwd()
	return d
}

// hookInput is the subset of a Claude Code hook's stdin JSON the hooks read.
// ToolUseID is empty today — hook stdin carries no tool_use_id — but is parsed
// if it ever appears.
type hookInput struct {
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
	Prompt    string          `json:"prompt"`
}

// readHookInput parses a hook's stdin JSON, tolerating an empty body.
func readHookInput(r io.Reader) hookInput {
	b, err := io.ReadAll(r)
	if err != nil || len(b) == 0 {
		return hookInput{}
	}
	var in hookInput
	_ = json.Unmarshal(b, &in)
	return in
}

// openURL opens url in the default browser.
func openURL(ctx context.Context, url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	if out, err := exec.CommandContext(ctx, name, url).CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w: %s", url, err, out)
	}
	return nil
}
