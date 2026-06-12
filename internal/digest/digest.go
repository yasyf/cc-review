// Package digest computes the cross-language tool digest shared with
// cc-transcript: sha256 over the RFC 8785 (JCS) canonicalization of
// {"input": <raw tool_input>, "tool": <tool name>}. Parity with the Python
// implementation is gated by testdata/digest_fixtures.json, generated from
// the Python source of truth.
package digest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

type document struct {
	Input json.RawMessage `json:"input"`
	Tool  string          `json:"tool"`
}

// Tool returns the 64-char lowercase hex digest for one tool call. The raw
// input is spliced verbatim into the wrapper document and canonicalized as
// JSON text, never round-tripped through Go values, so number fidelity is
// preserved.
func Tool(toolName string, toolInput json.RawMessage) (string, error) {
	if len(toolInput) == 0 {
		return "", fmt.Errorf("digest tool %s: empty tool input", toolName)
	}
	doc, err := json.Marshal(document{Input: toolInput, Tool: toolName})
	if err != nil {
		return "", fmt.Errorf("marshal digest document for %s: %w", toolName, err)
	}
	canonical, err := jsoncanonicalizer.Transform(doc)
	if err != nil {
		return "", fmt.Errorf("canonicalize digest document for %s: %w", toolName, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}
