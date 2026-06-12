package digest

import (
	"encoding/json"
	"os"
	"testing"
)

type fixture struct {
	Tool   string          `json:"tool"`
	Input  json.RawMessage `json:"input"`
	Digest string          `json:"digest"`
}

func TestToolMatchesPythonFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/digest_fixtures.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var fixtures []fixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("fixture corpus is empty")
	}
	for _, f := range fixtures {
		t.Run(f.Tool, func(t *testing.T) {
			got, err := Tool(f.Tool, f.Input)
			if err != nil {
				t.Fatalf("Tool(%s): %v", f.Tool, err)
			}
			if got != f.Digest {
				t.Errorf("digest mismatch:\n got  %s\n want %s", got, f.Digest)
			}
		})
	}
}

func TestToolRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input json.RawMessage
	}{
		{"nil input", nil},
		{"malformed json", json.RawMessage(`{"unterminated`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Tool("Bash", tc.input); err == nil {
				t.Errorf("expected error, got digest %s", got)
			}
		})
	}
}
