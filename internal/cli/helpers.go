package cli

import (
	"encoding/json"
	"io"
	"os"
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
type hookInput struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	Prompt         string `json:"prompt"`
	TranscriptPath string `json:"transcript_path"`
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
