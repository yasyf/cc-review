package cli

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestStartExtraLines(t *testing.T) {
	organize := json.RawMessage(`{"id":"7","source":"system","prompt":"Organize this review into chapters and rate per-file risk."}`)
	for _, tc := range []struct {
		name     string
		offer    bool
		reason   string
		offerErr error
		organize json.RawMessage
		want     []string
	}{
		{
			name:   "offer with organize",
			offer:  true,
			reason: "development-channels session not yet approved",
			want: []string{
				`setup: {"offer":true,"reason":"development-channels session not yet approved"}`,
				`organize: ` + string(organize),
			},
			organize: organize,
		},
		{
			name:   "no offer no organize",
			offer:  false,
			reason: "already approved",
			want:   []string{`setup: {"offer":false,"reason":"already approved"}`},
		},
		{
			name:     "offer error degrades to offer false with reason",
			offer:    true,
			reason:   "ignored",
			offerErr: errors.New(`stat "/Library/Application Support": denied`),
			want:     []string{`setup: {"offer":false,"reason":"stat \"/Library/Application Support\": denied"}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := startExtraLines(tc.offer, tc.reason, tc.offerErr, tc.organize)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
			var setup struct {
				Offer  bool   `json:"offer"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(got[0], "setup: ")), &setup); err != nil {
				t.Fatalf("setup line is not valid JSON: %v", err)
			}
		})
	}
}
