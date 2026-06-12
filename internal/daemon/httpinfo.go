package daemon

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/yasyf/cc-review/internal/paths"
)

// HTTPInfo is the handshake the daemon publishes so CLI stream consumers (watch,
// mcp-channel) and the Vite dev proxy can find the HTTP/SSE plane's ephemeral port.
type HTTPInfo struct {
	Port int `json:"port"`
}

// readHTTPInfo returns the last published handshake, or the zero value when
// the file is absent, unreadable, or corrupt. The prior port is only a reuse
// hint for listenHTTP, so this is the one read where silent-zero is correct.
func readHTTPInfo() HTTPInfo {
	b, err := os.ReadFile(paths.HTTPInfoPath())
	if err != nil {
		return HTTPInfo{}
	}
	var info HTTPInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return HTTPInfo{}
	}
	return info
}

func writeHTTPInfo(info HTTPInfo) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := os.WriteFile(paths.HTTPInfoPath(), b, 0o600); err != nil {
		return fmt.Errorf("write http info: %w", err)
	}
	return nil
}
