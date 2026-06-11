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
		name         string
		channelState string
		offer        bool
		reason       string
		offerErr     error
		organize     json.RawMessage
		want         []string
	}{
		{
			name:         "active offer with organize",
			channelState: "active",
			offer:        true,
			reason:       "development-channels session not yet approved",
			want: []string{
				"channel: active",
				`setup: {"offer":true,"reason":"development-channels session not yet approved"}`,
				`organize: ` + string(organize),
			},
			organize: organize,
		},
		{
			name:         "pending no offer no organize",
			channelState: "pending",
			offer:        false,
			reason:       "already approved",
			want: []string{
				"channel: pending",
				`setup: {"offer":false,"reason":"already approved"}`,
			},
		},
		{
			name:         "inactive offer error degrades to offer false with reason",
			channelState: "inactive",
			offer:        true,
			reason:       "ignored",
			offerErr:     errors.New(`stat "/Library/Application Support": denied`),
			want: []string{
				"channel: inactive",
				`setup: {"offer":false,"reason":"stat \"/Library/Application Support\": denied"}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := startExtraLines(tc.channelState, tc.offer, tc.reason, tc.offerErr, tc.organize)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
			var setup struct {
				Offer  bool   `json:"offer"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(got[1], "setup: ")), &setup); err != nil {
				t.Fatalf("setup line is not valid JSON: %v", err)
			}
		})
	}
}
