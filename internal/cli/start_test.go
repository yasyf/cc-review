package cli

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/daemon"
)

func TestStartExtraLines(t *testing.T) {
	organize := json.RawMessage(`{"id":"7","source":"system","prompt":"Organize this review into chapters and rate per-file risk."}`)
	userReq := json.RawMessage(`{"id":"8","source":"user","prompt":"mark all mechanical changes as viewed"}`)
	for _, tc := range []struct {
		name         string
		channelState string
		offer        bool
		reason       string
		offerErr     error
		stack        *daemon.StackInfo
		organizes    []json.RawMessage
		want         []string
	}{
		{
			name:         "active offer with organize",
			channelState: "active",
			offer:        true,
			reason:       "channel not yet approved",
			want: []string{
				"channel: active",
				`setup: {"offer":true,"reason":"channel not yet approved"}`,
				`organize: ` + string(organize),
			},
			organizes: []json.RawMessage{organize},
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
		{
			name:         "multiple requests emit one organize line each",
			channelState: "active",
			offer:        false,
			reason:       "already approved",
			organizes:    []json.RawMessage{organize, userReq},
			want: []string{
				"channel: active",
				`setup: {"offer":false,"reason":"already approved"}`,
				`organize: ` + string(organize),
				`organize: ` + string(userReq),
			},
		},
		{
			name:         "stack review emits a stack line after setup",
			channelState: "active",
			offer:        false,
			reason:       "already approved",
			stack:        &daemon.StackInfo{Trunk: "main", Branches: []string{"feat-a", "feat-b"}},
			want: []string{
				"channel: active",
				`setup: {"offer":false,"reason":"already approved"}`,
				`stack: {"trunk":"main","branches":["feat-a","feat-b"]}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := startExtraLines(tc.channelState, tc.offer, tc.reason, tc.offerErr, tc.stack, tc.organizes)
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
