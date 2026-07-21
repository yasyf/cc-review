package daemon

import (
	"encoding/json"
	"testing"
)

func TestDecodeBodyRequiresExactProtocolV1(t *testing.T) {
	const organization = `{"overview":null,"chapters":[{"title":"t","summary":"s","files":[{"path":"a.go","risk":"low","rationale":"r","focus":"f","lines":[]}]}]}`
	if got, err := decodeBody(json.RawMessage(`{"version_number":1,"organization":` + organization + `}`)); err != nil || got.Organization == nil {
		t.Fatalf("decode exact v1 body = %+v, %v", got, err)
	}

	for _, body := range []string{
		`{"version_number":1,"organization":{"overview":null,"chapters":[{"title":"t","summary":"s","files":[{"path":"a.go","risk":"low","rationale":"r","lines":[]}]}]}}`,
		`{"unknown":true}`,
		`{} {}`,
	} {
		if _, err := decodeBody(json.RawMessage(body)); err == nil {
			t.Fatalf("decodeBody(%s) succeeded", body)
		}
	}
}
