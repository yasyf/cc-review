package daemon

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/yasyf/cc-review/internal/paths"
)

// HTTPInfo is the handshake the daemon publishes so CLI stream consumers (watch,
// mcp-channel) can reach the HTTP/SSE plane on its ephemeral port.
type HTTPInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
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

// ReadHTTPInfo reads the daemon's published HTTP handshake.
func ReadHTTPInfo() (HTTPInfo, error) {
	b, err := os.ReadFile(paths.HTTPInfoPath())
	if err != nil {
		return HTTPInfo{}, fmt.Errorf("read http info: %w", err)
	}
	var info HTTPInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return HTTPInfo{}, fmt.Errorf("parse http info: %w", err)
	}
	return info, nil
}
